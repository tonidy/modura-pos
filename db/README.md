# Database

Plain SQL, applied in filename order. The schema is shared by any provider that implements the contracts, which is why it lives here and not inside the Go app.

| File | What it adds |
|---|---|
| `migrations/0001_catalog_inventory.sql` | `products`, `inventory` |
| `migrations/0002_sales.sql` | `sales`, `sale_lines`, `stock_movements`, `idempotency_keys` |
| `migrations/0003_demo_restock.sql` | `inventory.restock_to`, the level the demo replenishment job tops up to |
| `migrations/0004_contract_releases.sql` | `contract_releases`, append-only (a trigger rejects updates and deletes) |
| `migrations/0005_outbox.sql` | `outbox`, the transactional outbox for events such as `sales.saleCompleted`, append-only, with the writing transaction in `tx` ([ADR-0011](../docs/adr/0011-reports-service.md)) |
| `migrations/0006_drop_contract_releases.sql` | drops `contract_releases`: the registry moved to `apps/registry-go` and its own schema (ADR-0013) |
| `seed/demo.sql` | The deterministic demo catalog: 8 products, one inactive, croissants nearly sold out |

## How migrations are applied

There is one runner, `apps/sales-go/internal/dbmigrate`. It records applied files in `schema_migrations`, applies the rest in order (one transaction each, under an advisory lock), and loads the seed only into an empty catalog.

- Locally: `pnpm db:reset` drops the `public` and `reports` schemas, then runs `sales-api migrate`. It is destructive on purpose.
- The `reports` schema is not here. It belongs to the Python service, which keeps its own migrations in `apps/reports-py/migrations` and applies them on start. It reads `outbox` and nothing else from `public`.
- In Go tests: `apps/sales-go/internal/testdb` creates a throwaway schema per test and runs the same migrations, so tests run in parallel against real Postgres and cover the migrations too.
- In the live demo: `sales-api serve` migrates on start, so a deploy needs no separate step. See [docs/deploy.md](../docs/deploy.md).

## Rules

- Never edit a migration that has been applied anywhere shared. Add a new numbered file instead.
- Constraints are safety nets, not business logic. Checkout must never rely on hitting `CHECK (available >= 0)`. See [ADR-0004](../docs/adr/0004-create-sale-idempotency.md).
- Money columns are `bigint` minor units plus a `currency` code. See [ADR-0006](../docs/adr/0006-money.md).
- Every stock change writes a `stock_movements` row, so `inventory.available` can be audited.
