"""The reports API against its contract (plan M7): every response is validated
against generated/openapi/reports-py.json by `call`."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta

import pytest

from modura_reports.config import ROOT
from modura_reports.consumer import consume_once
from modura_reports.errors import CATEGORY_STATUS, ERROR_CATALOG

from .conftest import CASHIER, VIEWER, call, publish, sale_payload

pytestmark = pytest.mark.anyio

TODAY = datetime.now(UTC).replace(hour=12, minute=0, second=0, microsecond=0)


async def seed(pool) -> None:
    async with pool.connection() as conn:
        await publish(conn, sale_payload("sale_a", TODAY, [("prd_croissant", 2, 375), ("prd_espresso", 1, 300)]))
        await publish(conn, sale_payload("sale_b", TODAY, [("prd_espresso", 3, 300)]))
        await publish(conn, sale_payload("sale_old", TODAY - timedelta(days=3), [("prd_flat_white", 5, 450)]))
    async with pool.connection() as conn:
        assert await consume_once(conn) == 3


async def test_empty_daily_summary(client, contract, pool):
    response = await call(client, contract, VIEWER, "reports.dailySummary", {})

    assert response.status_code == 200
    body = response.json()
    assert body["salesCount"] == 0 and body["revenue"] == [] and body["topProducts"] == []
    assert "asOf" not in body  # nothing processed yet, and the contract makes it optional


async def test_daily_summary_after_events(client, contract, pool):
    await seed(pool)

    body = (await call(client, contract, CASHIER, "reports.dailySummary", {"date": TODAY.date().isoformat()})).json()

    assert body["salesCount"] == 2
    assert body["itemsSold"] == 6
    assert body["revenue"] == [{"amountMinor": 1950, "currency": "USD"}]
    assert [p["productId"] for p in body["topProducts"]] == ["prd_espresso", "prd_croissant"]
    assert body["topProducts"][0] == {
        "productId": "prd_espresso",
        "quantity": 4,
        "revenue": {"amountMinor": 1200, "currency": "USD"},
    }
    assert body["asOf"]


async def test_top_products_over_a_range(client, contract, pool):
    await seed(pool)
    start = (TODAY - timedelta(days=7)).date().isoformat()

    body = (
        await call(client, contract, VIEWER, "reports.topProducts", {"from": start, "to": TODAY.date().isoformat(), "limit": 2})
    ).json()

    assert body["from"] == start
    assert [p["productId"] for p in body["products"]] == ["prd_flat_white", "prd_espresso"]


async def test_invalid_ranges_are_domain_errors(client, contract):
    reversed_range = await call(client, contract, VIEWER, "reports.topProducts", {"from": "2026-10-10", "to": "2026-10-01"})
    too_long = await call(client, contract, VIEWER, "reports.topProducts", {"from": "2026-01-01", "to": "2026-10-01"})

    assert reversed_range.status_code == 422 and reversed_range.json()["code"] == "reports.INVALID_RANGE"
    assert too_long.status_code == 422 and too_long.json()["code"] == "reports.INVALID_RANGE"


async def test_authentication_authorization_and_validation_come_from_the_contract(client, contract):
    missing = await call(client, contract, None, "reports.dailySummary", {})
    wrong = await call(client, contract, "nope", "reports.dailySummary", {})
    bad_date = await call(client, contract, VIEWER, "reports.dailySummary", {"date": "10/10/2026"})
    unknown_field = await call(client, contract, VIEWER, "reports.dailySummary", {"day": "2026-10-10"})

    assert missing.status_code == wrong.status_code == 401
    assert missing.json()["code"] == "UNAUTHENTICATED"
    assert bad_date.status_code == unknown_field.status_code == 400
    assert bad_date.json()["code"] == unknown_field.json()["code"] == "VALIDATION_FAILED"
    assert bad_date.json()["operation"] == "reports.dailySummary"
    assert bad_date.json()["requestId"].startswith("req_")


def test_error_catalog_matches_the_ir():
    ir = json.loads((ROOT / "generated" / "ir" / "modura.ir.json").read_text())
    contract = dict(ir["platformErrors"])
    for code, definition in ir["modules"]["reports"]["errors"].items():
        contract[f"reports.{code}"] = definition

    for code, (category, retryable) in ERROR_CATALOG.items():
        assert code in contract, f"{code} is not defined by the contract"
        assert (contract[code]["category"], contract[code]["retryable"]) == (category, retryable), code
        assert category in CATEGORY_STATUS
