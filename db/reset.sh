#!/usr/bin/env bash
# Drops the local database schemas, then lets the Go migration runner (the same one
# the deployed API and the tests use) apply every migration and load the demo seed.
# The reports service's schema goes too, since its projection is built from the
# outbox being dropped; restart `pnpm reports` and it migrates itself again.
# Local development only: it talks to the compose `postgres` service.
set -euo pipefail

cd "$(dirname "$0")/.."

compose=(${COMPOSE:-docker compose})
port="${MODURA_PG_PORT:-55433}"

"${compose[@]}" exec -T postgres \
  psql --quiet --set ON_ERROR_STOP=1 -U modura -d modura \
  -c 'DROP SCHEMA IF EXISTS reports CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public;'

DATABASE_URL="${DATABASE_URL:-postgres://modura:modura@localhost:${port}/modura?sslmode=disable}" \
  go -C apps/sales-go run ./cmd/sales-api migrate
