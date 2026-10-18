"""Applies sales.saleCompleted events to the reports read model."""

from __future__ import annotations

from typing import Any
from uuid import UUID

from psycopg import AsyncConnection

from .models import SalesSaleCompletedEvent

SALE_COMPLETED = "sales.saleCompleted"


async def apply_sale_completed(conn: AsyncConnection, event_id: UUID, payload: dict[str, Any]) -> bool:
    """Applies one event inside the caller's transaction. Returns False for an event
    already applied: delivery is at least once, and the inbox makes a duplicate a no-op.

    The payload model ignores fields it does not know, as docs/stability.md requires of
    event consumers, so a producer adding a field cannot break this service.
    """
    claim = await conn.execute(
        "INSERT INTO reports.inbox (event_id) VALUES (%s) ON CONFLICT (event_id) DO NOTHING", (event_id,)
    )
    if claim.rowcount == 0:
        return False

    sale = SalesSaleCompletedEvent.model_validate(payload)
    await conn.execute(
        "INSERT INTO reports.sales (sale_id, sold_at, items, total_minor, currency) VALUES (%s, %s, %s, %s, %s)",
        (
            sale.id.root,
            sale.createdAt,
            sum(line.quantity for line in sale.lines),
            sale.total.amountMinor,
            sale.total.currency,
        ),
    )
    async with conn.cursor() as cursor:
        await cursor.executemany(
            "INSERT INTO reports.sale_lines (sale_id, product_id, quantity, line_total_minor, currency) "
            "VALUES (%s, %s, %s, %s, %s)",
            [
                (sale.id.root, line.productId.root, line.quantity, line.lineTotal.amountMinor, line.lineTotal.currency)
                for line in sale.lines
            ],
        )
    return True
