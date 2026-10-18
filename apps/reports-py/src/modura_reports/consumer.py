"""Reads the Go service's transactional outbox (ADR-0011).

Outbox ids come from a sequence, so a transaction can take id 9, commit after the
one that took id 10, and a consumer that remembers "last id seen" would skip 9 for
good. Instead, every outbox row records its writing transaction (`tx`, an xid8).

Each round reads every committed row from the saved horizon up, then moves the
horizon to `pg_snapshot_xmin(pg_current_snapshot())`, the oldest transaction still
running. Everything below it has finished, so no row can still appear there; a row
of a transaction that is still running is invisible now and sits above the new
horizon, so a later round reads it. Rows above the horizon are read again on the
next round, and the inbox turns each repeat into a no-op.

The horizon is not the read limit, because xmin is cluster-wide: one long
transaction in any database on the same server (a backup, a stuck session, another
service's tests) holds it back. Reading only below it would stall the reports for
as long as that transaction runs. Reading every visible row keeps them current, and
the long transaction only makes the re-read window bigger.
"""

from __future__ import annotations

import asyncio
import logging

from psycopg import AsyncConnection
from psycopg_pool import AsyncConnectionPool

from .projection import SALE_COMPLETED, apply_sale_completed

CONSUMER = "reports.sales"
SUBSCRIBED = [SALE_COMPLETED]

log = logging.getLogger("modura.reports.consumer")


async def consume_once(conn: AsyncConnection) -> int:
    """Applies every committed event from the saved horizon up, in one transaction;
    returns how many were new. Safe to run concurrently: the state row is locked."""
    async with conn.transaction():
        await conn.execute(
            "INSERT INTO reports.consumer_state (consumer, horizon) VALUES (%s, '0') ON CONFLICT DO NOTHING",
            (CONSUMER,),
        )
        state = await (
            await conn.execute(
                "SELECT horizon::text FROM reports.consumer_state WHERE consumer = %s FOR UPDATE", (CONSUMER,)
            )
        ).fetchone()
        start = state[0]
        # Taken before reading: every transaction below it has finished by now, so the
        # read that follows sees all of their rows.
        horizon = (await (await conn.execute("SELECT pg_snapshot_xmin(pg_current_snapshot())::text")).fetchone())[0]

        rows = await (
            await conn.execute(
                "SELECT event_id, event, payload, occurred_at FROM public.outbox "
                "WHERE tx >= %s::xid8 AND event = ANY(%s) ORDER BY tx, id",
                (start, SUBSCRIBED),
            )
        ).fetchall()

        applied = 0
        latest = None
        for event_id, event, payload, occurred_at in rows:
            if event == SALE_COMPLETED and await apply_sale_completed(conn, event_id, payload):
                applied += 1
            latest = occurred_at if latest is None else max(latest, occurred_at)

        await conn.execute(
            "UPDATE reports.consumer_state SET horizon = GREATEST(horizon, %s::xid8), "
            "last_event_at = GREATEST(last_event_at, %s) WHERE consumer = %s",
            (horizon, latest, CONSUMER),
        )
    return applied


async def run(pool: AsyncConnectionPool, interval_seconds: float, stop: asyncio.Event) -> None:
    """Polls until `stop` is set. A failed round is logged and retried: a broken
    consumer must never take the API down."""
    while not stop.is_set():
        try:
            async with pool.connection() as conn:
                if applied := await consume_once(conn):
                    log.info("applied %d event(s)", applied)
        except Exception:
            log.exception("consumer round failed")
        try:
            await asyncio.wait_for(stop.wait(), timeout=interval_seconds)
        except TimeoutError:
            pass
