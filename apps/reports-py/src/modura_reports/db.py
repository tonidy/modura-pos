"""Connection pool and the reports schema's own migrations."""

from __future__ import annotations

from pathlib import Path

from psycopg import AsyncConnection
from psycopg_pool import AsyncConnectionPool


def create_pool(database_url: str) -> AsyncConnectionPool:
    return AsyncConnectionPool(database_url, min_size=1, max_size=8, open=False)


async def migrate(conn: AsyncConnection, migrations_dir: Path) -> list[str]:
    """Applies pending files from migrations/ in order, each in its own transaction,
    under an advisory lock so concurrent starts do not race."""
    applied: list[str] = []
    await conn.execute("SELECT pg_advisory_lock(hashtext('reports.migrations'))")
    try:
        async with conn.transaction():
            await conn.execute("CREATE SCHEMA IF NOT EXISTS reports")
            await conn.execute(
                "CREATE TABLE IF NOT EXISTS reports.schema_migrations "
                "(version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"
            )
        for file in sorted(migrations_dir.glob("*.sql")):
            version = file.stem
            cursor = await conn.execute("SELECT 1 FROM reports.schema_migrations WHERE version = %s", (version,))
            if await cursor.fetchone():
                continue
            async with conn.transaction():
                await conn.execute(file.read_text(encoding="utf-8").encode())
                await conn.execute("INSERT INTO reports.schema_migrations (version) VALUES (%s)", (version,))
            applied.append(version)
    finally:
        await conn.execute("SELECT pg_advisory_unlock(hashtext('reports.migrations'))")
    return applied
