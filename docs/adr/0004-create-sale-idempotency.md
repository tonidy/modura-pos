# ADR-0004: `sales.createSale` idempotency, transactions and concurrency

Status: Accepted (M2).

## Context

A checkout must deduct stock exactly once, even when the client retries after a timeout, the cashier double-clicks, or two registers sell the last unit at the same moment. This is the main correctness claim of the project.

## Decision

The client sends an `Idempotency-Key` (a UUID per checkout attempt). The server normalizes the request (merges lines for the same product, sorts by `product_id`), fingerprints it, and runs everything in one READ COMMITTED transaction with explicit row locks:

1. Claim the key: `INSERT INTO idempotency_keys ... ON CONFLICT (scope, key) DO NOTHING`, under `lock_timeout` = 2s. The scope is the caller's identity. The fingerprint is a SHA-256 of the operation name and the normalized lines.
2. Lock inventory rows with `SELECT ... ORDER BY product_id FOR UPDATE`, under `lock_timeout` = 5s. With `ORDER BY`, Postgres locks rows as they leave the sort, so every checkout takes locks in the same order and multi-item sales cannot deadlock each other.
3. Load the products and reject unknown, inactive or insufficient lines. Nothing has been written yet.
4. Insert the sale, sale lines with price snapshots and stock movements, and decrement inventory.
5. Store the response body on the idempotency row and commit.

Claiming the key before touching stock is the decision that matters. If the key were stored last, a same-key retry waiting on the inventory lock would wake up, see the stock the first request already took, and answer `INSUFFICIENT_STOCK` instead of returning the original sale. This was checked by moving the claim to the end: `TestInFlightRetryWaitsAndReplaysEvenAfterTheLastUnitIsSold` then fails with exactly that 422.

## Same-key behavior

| Situation | Response |
|---|---|
| Same key, same request, finished | The stored body, status 200, `Idempotent-Replayed: true`. Stock is not touched. |
| Same key, different request | `IDEMPOTENCY_KEY_REUSED` (409). |
| Same key while the first is in flight | The claim waits on the primary key. If the first commits, `ON CONFLICT DO NOTHING` inserts nothing and the stored result is replayed. If it rolls back, the insert succeeds and the waiting request becomes the owner. |
| The wait exceeds 2s | `IDEMPOTENCY_IN_PROGRESS` (409, retryable). |
| Inventory locks held past 5s | `UNAVAILABLE` (503, retryable). |

`ON CONFLICT DO NOTHING` was chosen over a plain `INSERT` because a unique violation aborts the transaction, which would force a second transaction just to read the stored result.

Only successful sales are stored. A rejected checkout (unknown product, inactive product, insufficient stock) rolls back the claim with everything else, so the key stays free. Retrying the same attempt after the problem is fixed works, and a rejection is never replayed after stock has been replenished.

## Safety nets

- `CHECK (available >= 0)` on inventory.
- Primary key `(scope, key)` on idempotency rows, and `UNIQUE (idempotency_scope, idempotency_key)` on sales.
- `CHECK (line_total_minor = unit_price_minor * quantity)` on sale lines.
- `CHECK ((reason = 'sale') = (sale_id IS NOT NULL))` on stock movements.

They catch bugs. The transaction logic never depends on them.

## Notes

- Every demo visitor shares the cashier identity, so all keys share one scope. With random UUIDs, collisions are not a practical concern.
- Merging lines can push a product past the per-line limit of 999. That is answered with `VALIDATION_FAILED` before the transaction starts.
- Idempotency rows are kept until the nightly demo reset. A retention job is not needed for v1.
- `Options.BeforeCommit` in the store is a test seam that holds a transaction open just before commit. It is how the in-flight cases are tested deterministically, without sleeps.

## Alternatives considered

- Conditional `UPDATE inventory SET available = available - $1 WHERE available >= $1`. It avoids explicit locks, but multi-line sales still need a consistent order, and per-line error details get awkward.
- SERIALIZABLE isolation with retry. Correct, but it moves the complexity into retry loops and makes failures harder to explain.
- Storing rejections as idempotent results, as some payment APIs do. Rejected because a stock problem is temporary, and replaying an old `INSUFFICIENT_STOCK` after a restock would be wrong for a POS.

## Evidence

`apps/sales-go/internal/httpapi/checkout_test.go`, run against real Postgres, ten times in a row with `-race` while this ADR was written:

- `TestCheckoutDeductsStockOnceAndSnapshotsPrices`: one deduction, merged lines, replay identical after a price change.
- `TestCheckoutRejectsAKeyReusedForADifferentSale`
- `TestRejectedCheckoutsWriteNothingAndLeaveTheKeyFree`
- `TestConcurrentCheckoutsCannotOversell`: 20 parallel buyers, 3 units, exactly 3 sales.
- `TestConcurrentRetriesWithOneKeyCreateOneSale`: 10 parallel retries, one sale, one original response.
- `TestConcurrentMultiItemCheckoutsDoNotDeadlock`: opposite line orders, all succeed.
- `TestInFlightRetryWaitsAndReplaysEvenAfterTheLastUnitIsSold`
- `TestInFlightRetryTakesOverWhenTheOriginalRollsBack`
- `TestInFlightRetryGivesUpAfterTheClaimTimeout`
