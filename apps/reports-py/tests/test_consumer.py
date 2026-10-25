"""The outbox consumer (plan M7, ADR-0011): at-least-once delivery made idempotent by
the inbox, and no event skipped when transactions commit out of id order."""

from __future__ import annotations

from datetime import UTC, datetime

import psycopg
import pytest

from modura_reports.consumer import consume_once
from modura_reports.queries import daily_summary

from .conftest import ADMIN_URL, publish, sale_payload

pytestmark = pytest.mark.anyio

NOW = datetime.now(UTC)


async def sales_count(pool) -> int:
    async with pool.connection() as conn:
        return (await daily_summary(conn, NOW.date())).salesCount


async def consume(pool) -> int:
    async with pool.connection() as conn:
        return await consume_once(conn)


async def test_a_redelivered_event_is_applied_once(pool):
    async with pool.connection() as conn:
        await publish(conn, sale_payload("sale_once", NOW, [("prd_espresso", 1, 300)]))
    assert await consume(pool) == 1

    # Redelivery: forget how far we read, as after a crash between reading and saving.
    async with pool.connection() as conn:
        await conn.execute("UPDATE reports.consumer_state SET horizon = '0'")
    assert await consume(pool) == 0
    assert await sales_count(pool) == 1


async def test_an_event_that_commits_after_a_later_one_is_not_skipped(pool, database_url):
    # Transaction A writes first (lower outbox id) and stays open; B writes later and commits.
    async with await psycopg.AsyncConnection.connect(database_url) as a:
        await publish(a, sale_payload("sale_early", NOW, [("prd_croissant", 1, 375)]))
        async with await psycopg.AsyncConnection.connect(database_url, autocommit=True) as b:
            await publish(b, sale_payload("sale_late", NOW, [("prd_espresso", 1, 300)]))

        # B is applied at once. A cursor on "last id seen" would now be past A and
        # skip it forever; the horizon stays below A's transaction instead.
        assert await consume(pool) == 1
        await a.commit()

    assert await consume(pool) == 1
    assert await sales_count(pool) == 2
    assert await consume(pool) == 0


async def test_an_open_transaction_elsewhere_on_the_server_does_not_stall_the_reports(pool):
    # The horizon (xmin) is cluster-wide, so a transaction in another database holds it
    # back, as the Go tests running in parallel in CI do. Events must still arrive.
    async with await psycopg.AsyncConnection.connect(ADMIN_URL) as elsewhere:
        await elsewhere.execute("SELECT pg_current_xact_id()")
        async with pool.connection() as conn:
            await publish(conn, sale_payload("sale_meanwhile", NOW, [("prd_espresso", 1, 300)]))

        assert await consume(pool) == 1
        assert await sales_count(pool) == 1
        assert await consume(pool) == 0
        await elsewhere.rollback()


async def test_a_rolled_back_event_never_appears(pool, database_url):
    async with await psycopg.AsyncConnection.connect(database_url) as a:
        await publish(a, sale_payload("sale_rolledback", NOW, [("prd_croissant", 1, 375)]))
        await a.rollback()
    async with pool.connection() as conn:
        await publish(conn, sale_payload("sale_kept", NOW, [("prd_espresso", 1, 300)]))

    assert await consume(pool) == 1
    assert await sales_count(pool) == 1


async def test_unknown_events_and_unknown_payload_fields_are_ignored(pool):
    async with pool.connection() as conn:
        await publish(conn, {"anything": True}, event="inventory.stockChanged")
        # A producer may add optional fields; consumers ignore them (docs/stability.md).
        await publish(conn, sale_payload("sale_newer", NOW, [("prd_espresso", 2, 300)], loyaltyPoints=12))

    assert await consume(pool) == 1
    assert await sales_count(pool) == 1


async def dead_letters(pool) -> list[tuple]:
    async with pool.connection() as conn:
        return await (
            await conn.execute("SELECT event_id, event, error FROM reports.dead_letters ORDER BY failed_at")
        ).fetchall()


async def test_a_poison_event_is_dead_lettered_and_the_rest_are_applied(pool):
    async with pool.connection() as conn:
        await publish(conn, sale_payload("sale_before", NOW, [("prd_espresso", 1, 300)]))
        poison = await publish(conn, {"id": "sale_broken"})
        await publish(conn, sale_payload("sale_after", NOW, [("prd_croissant", 1, 375)]))

    assert await consume(pool) == 2
    assert await sales_count(pool) == 2
    [(event_id, event, error)] = await dead_letters(pool)
    assert (event_id, event) == (poison, "sales.saleCompleted")
    assert "createdAt" in error

    # Later rounds re-read the poison event but neither retry nor record it again.
    async with pool.connection() as conn:
        await conn.execute("UPDATE reports.consumer_state SET horizon = '0'")
    assert await consume(pool) == 0
    assert len(await dead_letters(pool)) == 1


async def test_an_event_the_read_model_rejects_is_dead_lettered(pool):
    # Two events for one sale: the second breaks reports.sales' primary key, every time.
    async with pool.connection() as conn:
        await publish(conn, sale_payload("sale_twice", NOW, [("prd_espresso", 1, 300)]))
        second = await publish(conn, sale_payload("sale_twice", NOW, [("prd_espresso", 1, 300)]))

    assert await consume(pool) == 1
    assert [row[0] for row in await dead_letters(pool)] == [second]
    assert await sales_count(pool) == 1
