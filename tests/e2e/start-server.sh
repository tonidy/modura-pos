#!/usr/bin/env bash
# Builds the UI, prepares a fresh E2E database and serves API + UI from one process,
# the same way the deployed container does.
set -euo pipefail

cd "$(dirname "$0")"
root="$(cd ../.. && pwd)"

reports="http://localhost:${E2E_REPORTS_PORT:-18091}"
VITE_REPORTS_URL="$reports" pnpm --dir "$root" --filter @modura/pos-web run build
pnpm exec tsx create-db.ts

export DATABASE_URL="${E2E_DATABASE_URL:-postgres://modura:modura@localhost:${MODURA_PG_PORT:-55433}/modura_e2e?sslmode=disable}"
export MODURA_DB_DIR="$root/db"
export MODURA_WEB_DIR="$root/apps/pos-web/dist"
export MODURA_CSP_CONNECT_SRC="$reports"
export MODURA_CHECKOUTS_PER_MIN=0
export PORT="${E2E_PORT:-18081}"

go -C "$root/apps/sales-go" build -o "$root/apps/sales-go/bin/sales-api" ./cmd/sales-api
"$root/apps/sales-go/bin/sales-api" reset-demo
exec "$root/apps/sales-go/bin/sales-api" serve
