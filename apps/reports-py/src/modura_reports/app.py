"""The reports service: routes generated from its OpenAPI document, CORS, and the
outbox consumer running next to the API."""

from __future__ import annotations

import asyncio
import contextlib
import logging
from collections.abc import AsyncIterator, Awaitable, Callable
from datetime import UTC, date, datetime
from typing import Any

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from psycopg import AsyncConnection
from psycopg_pool import AsyncConnectionPool
from starlette.requests import Request
from starlette.responses import JSONResponse, Response

from . import consumer, queries
from .config import Settings
from .contract import Contract, demo_identities, new_request_id
from .db import create_pool, migrate
from .errors import INTERNAL, UNAVAILABLE, ModuraError, error_response
from .models import ReportsDailySummaryInput, ReportsTopProductsInput

log = logging.getLogger("modura.reports")

Handler = Callable[[AsyncConnection, dict[str, Any]], Awaitable[BaseModel]]


async def daily_summary(conn: AsyncConnection, body: dict[str, Any]) -> BaseModel:
    request = ReportsDailySummaryInput.model_validate(body)
    day = date.fromisoformat(request.date.root) if request.date else datetime.now(UTC).date()
    return await queries.daily_summary(conn, day)


async def top_products(conn: AsyncConnection, body: dict[str, Any]) -> BaseModel:
    request = ReportsTopProductsInput.model_validate(body)
    return await queries.top_products(
        conn, date.fromisoformat(request.from_.root), date.fromisoformat(request.to.root), request.limit or 10
    )


# Every operation in this service's OpenAPI document needs an entry; create_app
# refuses to start otherwise, the way Go's strict interface refuses to compile.
HANDLERS: dict[str, Handler] = {
    "reports.dailySummary": daily_summary,
    "reports.topProducts": top_products,
}


def create_app(settings: Settings, pool: AsyncConnectionPool | None = None) -> FastAPI:
    """Builds the app. Pass `pool` to manage the pool (and the consumer) yourself,
    as the tests do; otherwise the lifespan opens it, migrates, and starts the consumer."""
    contract = Contract.load(settings.openapi_path)
    missing = {op.id for op in contract.operations.values()} - HANDLERS.keys()
    if missing:
        raise RuntimeError(f"operations without a handler: {sorted(missing)}")
    identities = demo_identities(settings.cashier_token, settings.viewer_token)
    owns_pool = pool is None
    pool = pool or create_pool(settings.database_url)

    @contextlib.asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        stop = asyncio.Event()
        task = None
        if owns_pool:
            await pool.open(wait=True)
            async with pool.connection() as conn:
                applied = await migrate(conn, settings.migrations_dir)
                log.info("database ready, applied %s", applied)
            if settings.consumer_enabled:
                task = asyncio.create_task(consumer.run(pool, settings.poll_interval_seconds, stop))
        try:
            yield
        finally:
            stop.set()
            if task:
                await task
            if owns_pool:
                await pool.close()

    app = FastAPI(title="Modura reports", lifespan=lifespan, openapi_url=None, docs_url=None, redoc_url=None)
    app.state.pool = pool

    # Browsers call this service directly; the SDK routes reports.* here (plan M7).
    # Bearer tokens travel in a header, not cookies, so credentials stay off.
    app.add_middleware(
        CORSMiddleware,
        allow_origins=list(settings.cors_origins),
        allow_credentials=False,
        allow_methods=["POST"],
        allow_headers=["Authorization", "Content-Type"],
        expose_headers=["X-Request-Id"],
        max_age=600,
    )

    @app.middleware("http")
    async def request_id(request: Request, call_next: Callable[[Request], Awaitable[Response]]) -> Response:
        request.state.request_id = new_request_id()
        response = await call_next(request)
        response.headers["X-Request-Id"] = request.state.request_id
        return response

    async def handle(request: Request) -> Response:
        try:
            operation, body = await contract.admit(request, identities)
            async with pool.connection() as conn:
                result = await HANDLERS[operation.id](conn, body)
            return JSONResponse(result.model_dump(mode="json", by_alias=True, exclude_none=True))
        except ModuraError as error:
            return error_response(request, error.code, error.message, error.details)
        except OSError as error:
            log.error("database unavailable: %s", error)
            return error_response(request, UNAVAILABLE, "The database is temporarily unavailable.")
        except Exception:
            log.exception("request failed (requestId %s)", getattr(request.state, "request_id", "?"))
            return error_response(request, INTERNAL, "An unexpected error occurred.")

    for path in contract.operations:
        app.add_api_route(path, handle, methods=["POST"], include_in_schema=False)

    @app.get("/healthz")
    async def health() -> JSONResponse:
        """Dead letters make the status "degraded" but keep 200: the service is
        serving, and restarting it would not bring the missing sales back."""
        try:
            async with pool.connection() as conn:
                last = await queries.as_of(conn)
                dead, last_dead = await queries.dead_letters(conn)
        except Exception:
            return JSONResponse({"status": "database unavailable"}, status_code=503)
        consumer: dict[str, object] = {"asOf": last.isoformat() if last else None, "deadLetters": dead}
        if last_dead:
            consumer["lastDeadLetterAt"] = last_dead.isoformat()
        return JSONResponse({"status": "degraded" if dead else "ok", "consumer": consumer})

    return app
