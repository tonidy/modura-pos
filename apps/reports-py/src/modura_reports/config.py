"""Configuration from the environment. See docs/deploy.md for the full list."""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[4]


@dataclass(frozen=True)
class Settings:
    database_url: str
    port: int = 8090
    # Public demo tokens, the same as the Go service's (plan section 5).
    cashier_token: str = "demo-cashier"
    viewer_token: str = "demo-viewer"
    # Origins allowed to call this service from a browser. Never "*" (plan M7).
    cors_origins: tuple[str, ...] = ()
    consumer_enabled: bool = True
    poll_interval_seconds: float = 1.0
    openapi_path: Path = ROOT / "generated" / "openapi" / "reports-py.json"
    migrations_dir: Path = Path(__file__).resolve().parents[2] / "migrations"


def load() -> Settings:
    database_url = os.environ.get("DATABASE_URL")
    if not database_url:
        raise SystemExit("DATABASE_URL is required")
    origins = tuple(o.strip() for o in os.environ.get("MODURA_CORS_ORIGINS", "").split(",") if o.strip())
    if "*" in origins:
        raise SystemExit("MODURA_CORS_ORIGINS must list origins explicitly; '*' is not allowed")
    defaults = Settings(database_url=database_url)
    return Settings(
        database_url=database_url,
        port=int(os.environ.get("PORT", defaults.port)),
        cashier_token=os.environ.get("MODURA_CASHIER_TOKEN", defaults.cashier_token),
        viewer_token=os.environ.get("MODURA_VIEWER_TOKEN", defaults.viewer_token),
        cors_origins=origins,
        consumer_enabled=os.environ.get("MODURA_REPORTS_CONSUMER", "on") != "off",
        poll_interval_seconds=float(os.environ.get("MODURA_REPORTS_POLL_SECONDS", defaults.poll_interval_seconds)),
        openapi_path=Path(os.environ.get("MODURA_OPENAPI", defaults.openapi_path)),
    )
