# ADR-0011: Reports service in Python, fed by an outbox

Status: Accepted (M7).

## Context

Until M6 every module ran in the Go service. The roadmap had a Rust provider next, as a second implementation of `sales`. Nobody runs two sales backends, though, and a second implementation of the same module would only test what the shared compatibility fixtures already test. What had not been tested was the part a real product hits once it has more than one service: modules in different languages that depend on each other, own their data, and talk through events.

So the second language owns a new module. `reports` answers "how did we do today?", runs in Python (uv, FastAPI, pydantic, psycopg 3), and learns about sales only through the `sales.saleCompleted` event defined in M6.

## Decisions

**One OpenAPI document per service.** `modura.config.ts` maps every module to exactly one service (`sales-go`: catalog, inventory, sales, registry; `reports-py`: reports), and the CLI refuses a module owned twice or not at all. `generate` writes `generated/openapi/sales-go.json` and `reports-py.json`. A filtered document keeps only its modules' operations, the event payloads those modules publish or subscribe to, and the named types something kept refers to. Without the pruning, `oapi-codegen` would still generate Go types for reports. The IR and the TypeScript SDK still cover every module.

**The client routes itself, nothing forwards.** The SDK generator writes the owning service into each call (`transport.call("reports.dailySummary", input, { ...options, service: "reports-py" })`), and the HTTP transport picks a base URL per service from `serviceUrls`, falling back to `baseUrl`. MCP tools carry `_meta["modura/service"]` and the MCP server reads `MODURA_SERVICE_URLS`. Go never learns Python exists, and a test pins that: its embedded spec may not name a module it does not serve. Two alternatives were rejected. A forwarding route in Go would make Go a dependency of every reports call and need its own timeouts and error mapping. A gateway is one more thing to deploy, for a two-service demo.

**CORS on the Python service, not a shared origin.** The UI is served by Go, so its calls to reports are cross-origin. Python allows an explicit list of origins (`MODURA_CORS_ORIGINS`, and `*` is refused at startup), methods `POST`, request headers `Authorization` and `Content-Type`, exposes `X-Request-Id`, and caches preflights for 600 seconds. Tokens travel in a header, so credentials mode stays off. On the Go side the UI's CSP gains `connect-src` for origins listed in `MODURA_CSP_CONNECT_SRC`, a generic setting that does not name reports. Each entry must be a bare http(s) origin, so configuration cannot inject other directives. In development the UI calls Python directly too, instead of through a Vite proxy, so CORS breaks locally before it breaks anywhere else.

**Transactional outbox in Go.** `sales.createSale` inserts the event into `outbox` in the same transaction as the sale, with the sale as payload, so a rolled-back checkout leaves no event and an idempotent replay returns before writing one. The table is append-only through a trigger. The demo reset truncates sales but leaves the outbox alone: the event log is history, and the reports read model keeps it.

**Reading the outbox without skipping.** Outbox ids come from a sequence, and sequence order is not commit order: a transaction can take id 9, commit after the one that took id 10, and a consumer that remembers "last id seen" skips 9 for good. Each row instead records its writing transaction in `tx xid8 DEFAULT pg_current_xact_id()`. Each round takes `pg_snapshot_xmin(pg_current_snapshot())`, the oldest transaction still running, then reads every committed row with `tx` at or above the saved horizon, and saves xmin as the new horizon. Every transaction below xmin has finished, so no row can still appear below it. A row of a transaction that is still running is invisible during the round and sits above the new horizon, so a later round picks it up. Rows above the horizon are read again on the next round, and the inbox makes each repeat a no-op.

**xmin is not the read limit.** The first version read only rows below xmin. CI showed why that is wrong: xmin is cluster-wide, so an open transaction in any database on the same server holds it back. There it was the Go tests running in parallel, and in production it could be a backup or a session left idle in a transaction. The reports would stop for as long as that transaction ran. Reading every visible row keeps them current, and a long transaction elsewhere only makes the re-read window bigger. A test holds a transaction open in another database and requires events to arrive anyway.

**Idempotent projection.** Delivery is at least once: a consumer that applies events and crashes before saving its horizon reads them again. The projection runs in one transaction with the horizon update and claims each event in `reports.inbox` (primary key `event_id`) first, so a duplicate does nothing. Events are applied in `(tx, id)` order. The read model only sums, so order inside a batch does not change the result anyway.

**A bad event is set aside, visibly.** Each event is applied in its own savepoint. An event that fails the same way on every attempt (its payload does not match the event model, or the read model's constraints or types reject it) is rolled back alone, recorded in `reports.dead_letters` with the error, and claimed in the inbox so later rounds skip it. `/healthz` then reports `"status": "degraded"` with the number of dead letters, and the error is logged. Any other failure, such as a lost connection, still fails the whole round, which is retried.

The first version stopped the consumer at a bad event instead, so that a contract bug could not hide behind silently wrong numbers. But then one event held back every sale after it until someone intervened, and the only signal was `asOf` no longer moving. With dead letters, the reports stay current apart from the events set aside, and the health check names how many those are. A fixed event is replayed by deleting its rows in `reports.dead_letters` and `reports.inbox` and moving the horizon back to its `tx` (`migrations/0002_dead_letters.sql`).

**Python reads the contract the way Go does.** Request, response and event models are generated from `reports-py.json` with `datamodel-code-generator`, with a stale check. Event models ignore unknown fields, as `docs/stability.md` requires of consumers, while requests are still validated strictly by `openapi-core` against the same document. Authentication, permissions from `x-modura-requires`, validation and canonical errors are a small middleware over the OpenAPI, like Go's, and the app refuses to start if an operation in the document has no handler.

**Visible eventual consistency.** Every reports response has `asOf`, the time of the most recent sale the projection has applied, and the UI shows it. Numbers that trail the till are fine; numbers that pretend not to are not.

**Data ownership.** Python reads `public.outbox` and nothing else Go owns, and writes only the `reports` schema, which it migrates itself. Go never reads `reports`.

## Consequences

- A second service now depends on the shared database being reachable, which is acceptable for a demo but means "independent services" stops at the process boundary. Moving the outbox to a broker would change only the consumer.
- The UI degrades per service: with Python down, selling still works and only the "Today" panel says reports are unavailable.
- Deploying the Python service is not part of M7 (plan section 9), so the hosted demo would not have the panel until it is.

## Evidence

- `apps/sales-go/internal/httpapi/outbox_test.go`: one event per sale in the same transaction, none on rollback, rejection or replay, append-only.
- `apps/reports-py/tests/test_consumer.py`: redelivery applied once, an earlier id committing late is still applied, rolled-back rows never appear, unknown events and fields ignored. A transaction open in another database does not delay events. Removing the inbox check fails the redelivery test; switching to an id-style horizon fails the out-of-order test; reading only below xmin fails the out-of-order test and the open-transaction test. An invalid payload and a sale the read model rejects are dead-lettered once, and the events around them are still applied.
- `apps/reports-py/tests/test_api.py`: every response validated against `reports-py.json`, errors limited to the codes declared per status, the error catalog checked against the IR, and `/healthz` degraded by a dead letter.
- `apps/reports-py/tests/test_cors.py`: preflight granted to the UI origin, nothing granted to others.
- `apps/sales-go/internal/httpapi/ownership_test.go`, `packages/generator-openapi/src/index.test.ts`: Go's document names no module it does not serve.
- `packages/sdk/src/runtime.test.ts`, `packages/generator-sdk-ts/src/index.test.ts`, `packages/generator-mcp/src/index.test.ts`, `apps/mcp-server/src/server.test.ts`: per-service routing.
- `tests/e2e/checkout.spec.ts`, `tests/e2e/registry.spec.ts`: a UI sale shows up in the "Today" panel through a direct cross-origin call, and through the `reports_dailySummary` MCP tool.
