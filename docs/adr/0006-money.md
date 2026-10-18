# ADR-0006: Money representation

Status: Accepted (M2).

## Context

Floating-point money produces rounding bugs, and a POS that lets the client send prices lets anyone set their own price.

## Decision

- The DSL has a built-in `money` type and no floating-point number type at all.
- On the wire, money is `{ "amountMinor": 450, "currency": "USD" }`. `amountMinor` is an integer in the currency's minor unit (cents for USD).
- Amounts stay within the JavaScript safe-integer range. In Postgres they are `bigint`, in Go `int64`.
- `sales.createSale` takes product IDs and quantities only. The server reads the current catalog price inside the checkout transaction and snapshots it on each sale line, so later price changes never alter past sales.
- `lineTotal = unitPrice * quantity` and `total = sum(lineTotal)`, computed on the server. v1 has no tax or discounts, so no rounding is needed.
- The demo runs in a single currency (USD, two minor digits). If a sale ever mixes currencies, checkout fails with `INTERNAL`: that is bad catalog data, not a customer error.
- The database enforces the arithmetic too: `CHECK (line_total_minor = unit_price_minor * quantity)` on sale lines.

## Consequences

- Clients format money for display themselves, using the currency's minor-unit exponent.
- Tax, discounts and multi-currency all need explicit rounding rules. They are out of scope for v1 and will need a follow-up ADR.

## Evidence

- M1: the contract carries no client price at all. `SalesCreateSaleInput` lines are `{ productId, quantity }`, and the closed object schema makes the server reject an extra `price` field with `VALIDATION_FAILED`. Money is one shared `Money` schema in OpenAPI, `int64` in the generated Go types and `number` in the SDK.
- M2: `TestCheckoutDeductsStockOnceAndSnapshotsPrices` in `apps/sales-go/internal/httpapi/checkout_test.go` prices a sale from the catalog, changes the catalog price, then shows that the replay still returns the old unit price while a new sale uses the new one.
