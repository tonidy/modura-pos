# Deploying the live demo

The demo is one container (see `Dockerfile`): the Go API serves the contract under `/v1`, the built POS UI at `/`, and `/healthz`. It needs one Postgres database. Nothing else.

## Configuration

| Variable | Secret | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | yes | none | Postgres connection string. |
| `PORT` | no | `8080` | Listen port. |
| `MODURA_DB_DIR` | no | `/app/db` in the image | Migrations and seed. |
| `MODURA_WEB_DIR` | no | `/app/web` in the image | Built UI. Unset to serve the API only. |
| `MODURA_CLIENT_IP_HEADER` | no | none | Header the platform proxy sets with the client IP (`Fly-Client-IP` on Fly.io). Only set it behind a proxy that overwrites it, or clients can choose their own rate-limit bucket. |
| `MODURA_CHECKOUTS_PER_MIN` | no | `30` | Per-IP limit on commands. `0` disables it. |
| `MODURA_DEMO_MODE` | no | off | `true` turns on stock replenishment (every 30s) and the nightly reset. |
| `MODURA_DEMO_RESET_AT` | no | `03:00` | UTC time of the nightly reset. |
| `MODURA_CASHIER_TOKEN`, `MODURA_VIEWER_TOKEN` | no | `demo-cashier`, `demo-viewer` | Demo bearer tokens. They are public on purpose and baked into the UI build too (`VITE_CASHIER_TOKEN`, `VITE_VIEWER_TOKEN`). |
| `MODURA_CSP_CONNECT_SRC` | no | none | Other origins the UI may call, space-separated, such as the reports service. Each must be a bare `http(s)://host[:port]`. |

Never commit `DATABASE_URL`. Set it as a platform secret.

The nightly demo reset clears POS data only. The event outbox survives it, so the reports keep their history.

The image cannot be built yet: the `Dockerfile` installs the Modura platform with the rest of the workspace, and until the platform's first release is on JSR it comes from a sibling checkout (`../modura`) that a Docker build cannot see.

## Reports service (Python)

The image above does not include it yet; deploying it is outside M7. To run it next to the API (`pnpm reports` does this locally):

```sh
cd apps/reports-py
uv sync --locked
DATABASE_URL=... MODURA_CORS_ORIGINS=https://pos.example uv run python -m modura_reports
```

| Variable | Secret | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | yes | none | The same database as the API. The service reads `public.outbox` and owns the `reports` schema, which it migrates on start. |
| `PORT` | no | `8090` | Listen port. |
| `MODURA_CORS_ORIGINS` | no | none | Comma-separated origins allowed to call it from a browser, normally the UI's. `*` is refused at startup. |
| `MODURA_CASHIER_TOKEN`, `MODURA_VIEWER_TOKEN` | no | `demo-cashier`, `demo-viewer` | Must match the API's, since the UI sends the same token to both. |
| `MODURA_REPORTS_CONSUMER` | no | `on` | `off` serves the API without reading the outbox, for example on a second replica. |
| `MODURA_REPORTS_POLL_SECONDS` | no | `1` | How often the consumer reads the outbox. |
| `MODURA_OPENAPI` | no | `generated/openapi/reports-py.json` | The contract it serves. |

Build the UI with `VITE_REPORTS_URL` set to the service's public URL, and give the API the same origin in `MODURA_CSP_CONNECT_SRC`. `/healthz` shows `asOf`, the newest sale the projection has applied; if it stops moving while sales continue, the consumer is stuck (see the logs) or not running.

## What happens on start

`sales-api serve` applies pending migrations (guarded by an advisory lock), seeds the catalog if it is empty, then serves. A restart never duplicates the seed, and a deploy needs no separate migration step.

## Demo jobs

With `MODURA_DEMO_MODE=true`:

- Every 30 seconds, products down to a quarter of their `restock_to` level are topped back up. Each top-up is an ordinary `restock` row in `stock_movements`, rows are locked in product order with `SKIP LOCKED`, and sales and idempotency records are never touched.
- At `MODURA_DEMO_RESET_AT`, every table is truncated and the seed reloaded in one transaction. `TRUNCATE` waits for in-flight checkouts, so nothing is half-wiped.
- Failures are logged and shown in `/healthz` under `demo.lastError`. The API keeps serving either way.

`/healthz` reports `lastReplenishAt`, `lastResetAt` and `nextResetAt`.

Both jobs run inside the API process, so run exactly one instance. The in-memory rate limiter assumes the same.

## Fly.io

Prerequisites: a Fly.io account, `flyctl` logged in, and a Postgres database. Fly Managed Postgres works, and so does any hosted Postgres reachable from Fly (Neon, Supabase).

```sh
# Once
fly apps create <your-app-name>          # then set `app` in fly.toml to match
fly secrets set DATABASE_URL='postgres://...'

# Every deploy
fly deploy
curl -s https://<your-app-name>.fly.dev/healthz
```

`fly.toml` keeps one machine always running (`min_machines_running = 1`, `auto_stop_machines = "off"`), because a stopped machine would miss the nightly reset.

Operations:

- Logs: `fly logs`. They are JSON lines with `requestId`, matching the `requestId` in error responses.
- Reset the demo now: `fly ssh console -C "/app/sales-api reset-demo"`.
- Roll back: `fly releases` to find the previous image, then `fly deploy --image <image>`. Migrations only add things, so the previous release keeps working against the newer schema.

## Any other host

The same image runs anywhere that can run a container and reach Postgres (Railway, Render, a VPS with Docker):

```sh
docker build -t modura-pos .
docker run -p 8080:8080 \
  -e DATABASE_URL='postgres://...' \
  -e MODURA_DEMO_MODE=true \
  modura-pos
```

Behind a reverse proxy, set `MODURA_CLIENT_IP_HEADER` to the header that proxy overwrites (for nginx, typically `X-Forwarded-For`; the last entry is used).

## Local fallback

If the live demo is down, a reviewer can run everything locally. See "Quick start" in the README.
