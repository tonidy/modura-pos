"""The read side: reports.dailySummary and reports.topProducts."""

from __future__ import annotations

from datetime import UTC, date, datetime, time, timedelta

from psycopg import AsyncConnection

from .consumer import CONSUMER
from .errors import INVALID_RANGE, ModuraError
from .models import (
    CatalogProductId,
    Money,
    ReportsDaySummary,
    ReportsProductSales,
    ReportsReportDate,
    ReportsTopProductsOutput,
)

MAX_RANGE_DAYS = 92


def _bounds(first: date, last: date) -> tuple[datetime, datetime]:
    """[start of `first`, start of the day after `last`) in UTC."""
    return datetime.combine(first, time(), UTC), datetime.combine(last + timedelta(days=1), time(), UTC)


async def as_of(conn: AsyncConnection) -> datetime | None:
    row = await (
        await conn.execute("SELECT last_event_at FROM reports.consumer_state WHERE consumer = %s", (CONSUMER,))
    ).fetchone()
    return row[0] if row else None


async def dead_letters(conn: AsyncConnection) -> tuple[int, datetime | None]:
    """How many events the consumer set aside, and when the last one failed."""
    count, last = await (await conn.execute("SELECT count(*), max(failed_at) FROM reports.dead_letters")).fetchone()
    return count, last


async def _top_products(conn: AsyncConnection, start: datetime, end: datetime, limit: int) -> list[ReportsProductSales]:
    rows = await (
        await conn.execute(
            "SELECT l.product_id, sum(l.quantity), sum(l.line_total_minor), l.currency "
            "FROM reports.sale_lines l JOIN reports.sales s USING (sale_id) "
            "WHERE s.sold_at >= %s AND s.sold_at < %s "
            "GROUP BY l.product_id, l.currency "
            "ORDER BY sum(l.quantity) DESC, sum(l.line_total_minor) DESC, l.product_id "
            "LIMIT %s",
            (start, end, limit),
        )
    ).fetchall()
    return [
        ReportsProductSales(
            productId=CatalogProductId(product_id),
            quantity=quantity,
            revenue=Money(amountMinor=revenue, currency=currency),
        )
        for product_id, quantity, revenue, currency in rows
    ]


async def daily_summary(conn: AsyncConnection, day: date) -> ReportsDaySummary:
    start, end = _bounds(day, day)
    count, items = await (
        await conn.execute(
            "SELECT count(*), coalesce(sum(items), 0) FROM reports.sales WHERE sold_at >= %s AND sold_at < %s",
            (start, end),
        )
    ).fetchone()
    revenue = await (
        await conn.execute(
            "SELECT currency, sum(total_minor) FROM reports.sales WHERE sold_at >= %s AND sold_at < %s "
            "GROUP BY currency ORDER BY currency",
            (start, end),
        )
    ).fetchall()
    return ReportsDaySummary(
        date=ReportsReportDate(day.isoformat()),
        salesCount=count,
        itemsSold=items,
        revenue=[Money(amountMinor=total, currency=currency) for currency, total in revenue],
        topProducts=await _top_products(conn, start, end, 5),
        asOf=await as_of(conn),
    )


async def top_products(conn: AsyncConnection, first: date, last: date, limit: int) -> ReportsTopProductsOutput:
    if first > last:
        raise ModuraError(INVALID_RANGE, f"The range starts on {first} but ends earlier, on {last}.")
    if (last - first).days + 1 > MAX_RANGE_DAYS:
        raise ModuraError(INVALID_RANGE, f"The range covers more than {MAX_RANGE_DAYS} days.")
    start, end = _bounds(first, last)
    return ReportsTopProductsOutput(
        **{"from": ReportsReportDate(first.isoformat())},
        to=ReportsReportDate(last.isoformat()),
        products=await _top_products(conn, start, end, limit),
        asOf=await as_of(conn),
    )
