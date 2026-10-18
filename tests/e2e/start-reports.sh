#!/usr/bin/env bash
# Serves the Python reports service against the E2E database. It waits for the Go
# service first: start-server.sh recreates the database and applies the migrations
# that create the outbox this service reads.
set -euo pipefail

cd "$(dirname "$0")"
root="$(cd ../.. && pwd)"
api="http://localhost:${E2E_PORT:-18081}"

for _ in $(seq 1 360); do
  curl -sf "$api/healthz" >/dev/null && break
  sleep 0.5
done
curl -sf "$api/healthz" >/dev/null || { echo "the Go service never became healthy" >&2; exit 1; }

export DATABASE_URL="${E2E_DATABASE_URL:-postgres://modura:modura@localhost:${MODURA_PG_PORT:-55433}/modura_e2e?sslmode=disable}"
export PORT="${E2E_REPORTS_PORT:-18091}"
export MODURA_CORS_ORIGINS="$api"
export MODURA_REPORTS_POLL_SECONDS=0.2

exec uv --directory "$root/apps/reports-py" run --frozen python -m modura_reports
