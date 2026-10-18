"""Test setup: a throwaway database per test session with the real Go migrations
(public schema, including the outbox) and the reports migrations, emptied before
every test. Without Postgres the tests are skipped, unless MODURA_REQUIRE_DB is set."""

from __future__ import annotations

import json
import os
import uuid
from collections.abc import AsyncIterator, Iterator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import httpx
import psycopg
import pytest
from openapi_core.testing import MockRequest, MockResponse
from psycopg.conninfo import make_conninfo
from psycopg_pool import AsyncConnectionPool

from modura_reports.app import create_app
from modura_reports.config import ROOT, Settings
from modura_reports.contract import Contract
from modura_reports.db import migrate
from modura_reports.models import SalesSaleCompletedEvent

ADMIN_URL = os.environ.get("DATABASE_URL", "postgresql://modura:modura@localhost:55433/modura?sslmode=disable")
UI_ORIGIN = "http://localhost:18081"
CASHIER, VIEWER = "cashier-token", "viewer-token"
MIGRATIONS = Path(__file__).resolve().parents[1] / "migrations"


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture(scope="session")
def database_url() -> Iterator[str]:
    name = f"reports_test_{uuid.uuid4().hex[:8]}"
    try:
        admin = psycopg.connect(ADMIN_URL, autocommit=True)
    except psycopg.OperationalError as error:
        if os.environ.get("MODURA_REQUIRE_DB"):
            raise
        pytest.skip(f"Postgres not reachable ({error}). Run `pnpm db:up` or set DATABASE_URL.")
    admin.execute(f"CREATE DATABASE {name}")
    url = make_conninfo(ADMIN_URL, dbname=name)
    with psycopg.connect(url, autocommit=True) as conn:
        # The Go service's schema, from the same files its migration runner applies.
        for file in sorted((ROOT / "db" / "migrations").glob("*.sql")):
            conn.execute(file.read_text(encoding="utf-8").encode())
    yield url
    admin.execute(f"DROP DATABASE {name} WITH (FORCE)")
    admin.close()


@pytest.fixture
async def pool(database_url: str) -> AsyncIterator[AsyncConnectionPool]:
    pool = AsyncConnectionPool(database_url, min_size=1, max_size=6, open=False)
    await pool.open(wait=True)
    async with pool.connection() as conn:
        await migrate(conn, MIGRATIONS)
        # TRUNCATE bypasses the outbox's append-only row trigger, as it should for tests.
        await conn.execute(
            "TRUNCATE reports.sale_lines, reports.sales, reports.inbox, reports.consumer_state, public.outbox"
        )
    yield pool
    await pool.close()


@pytest.fixture
def settings(database_url: str) -> Settings:
    return Settings(
        database_url=database_url,
        cashier_token=CASHIER,
        viewer_token=VIEWER,
        cors_origins=(UI_ORIGIN,),
        consumer_enabled=False,
    )


@pytest.fixture
async def client(settings: Settings, pool: AsyncConnectionPool) -> AsyncIterator[httpx.AsyncClient]:
    app = create_app(settings, pool)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://reports.test") as http:
        yield http


@pytest.fixture(scope="session")
def contract() -> Contract:
    return Contract.load(ROOT / "generated" / "openapi" / "reports-py.json")


async def call(
    client: httpx.AsyncClient, contract: Contract, token: str | None, operation: str, body: dict[str, Any]
) -> httpx.Response:
    """Sends a request and checks the response against the reports OpenAPI document:
    declared status, matching body, and an error code listed for that status."""
    headers = {"Authorization": f"Bearer {token}"} if token else {}
    response = await client.post(f"/v1/{operation}", json=body, headers=headers)

    request = MockRequest("http://reports.test", "post", f"/v1/{operation}", headers=headers, data=json.dumps(body))
    contract.openapi.validate_response(
        request, MockResponse(response.content, status_code=response.status_code, headers=dict(response.headers))
    )
    if response.status_code >= 400:
        declared = contract.document["paths"][f"/v1/{operation}"]["post"]["responses"][str(response.status_code)]
        assert response.json()["code"] in declared["x-modura-error-codes"]
    return response


def sale_payload(sale_id: str, sold_at: datetime, lines: list[tuple[str, int, int]], **extra: Any) -> dict[str, Any]:
    """A sales.saleCompleted payload, checked against the contract's event model.
    `lines` are (productId, quantity, unit price in cents)."""
    payload = {
        "id": sale_id,
        "createdAt": sold_at.astimezone(UTC).isoformat().replace("+00:00", "Z"),
        "lines": [
            {
                "productId": product,
                "quantity": quantity,
                "unitPrice": {"amountMinor": price, "currency": "USD"},
                "lineTotal": {"amountMinor": price * quantity, "currency": "USD"},
            }
            for product, quantity, price in lines
        ],
        "total": {"amountMinor": sum(q * p for _, q, p in lines), "currency": "USD"},
        **extra,
    }
    SalesSaleCompletedEvent.model_validate(payload)
    return payload


async def publish(conn: psycopg.AsyncConnection, payload: dict[str, Any], event: str = "sales.saleCompleted") -> uuid.UUID:
    """Writes an outbox row the way the Go service does. Commits only if `conn` does."""
    event_id = uuid.uuid4()
    await conn.execute(
        "INSERT INTO public.outbox (event_id, event, payload) VALUES (%s, %s, %s)",
        (event_id, event, json.dumps(payload)),
    )
    return event_id
