# ADR-0005: Generated SDK boundary

Status: Accepted (M3).

## Context

The type-safety story only works if the UI really goes through the generated client. One handwritten `fetch` call would quietly bypass it. At the same time, client-side types are not security: anyone can call the API directly.

## Decision

- The POS UI calls Modura operations only through the generated TypeScript SDK. No handwritten HTTP layer.
- This is enforced mechanically with oxlint in `apps/pos-web` (`.oxlintrc.json`): `fetch`, `window.fetch`, `globalThis.fetch`, `XMLHttpRequest`, `EventSource` and common HTTP client packages are errors, and `pnpm verify` runs the lint. oxlint was chosen over ESLint because typescript-eslint does not support TypeScript 7 yet.
- The SDK gives types and transport. It does not duplicate business rules. Stock checks, price selection, authorization and idempotency live in the Go provider only.
- The UI may show friendly hints (for example, disabling "add" at zero stock), but the server is the only authority, and the UI must handle every error the IR lists for the operation.

## SDK shape (decided in M1)

- The package is split in two. `generated.ts` holds types, per-operation error-code unions and `createModuraClient`, all derived from the IR. `runtime.ts` is a small handwritten transport, and it is the only place that calls `fetch`.
- Operations return `Result<Value, Code>` instead of throwing, so TypeScript makes callers look at the error case. `Code` is the exact union the IR lists for that operation.
- Idempotent operations take `IdempotentCallOptions` with a required `idempotencyKey`. Forgetting it is a compile error, not a runtime 400.
- Network failures become `UNAVAILABLE` and malformed error bodies become `INTERNAL`. Both are in every operation's union, so the transport never returns a code the caller was not told about.
- The SDK does no runtime validation of inputs. The server validates against the same schemas, and duplicating that in the browser would be a second implementation to keep in sync.

## Consequences

- A contract change shows up as a TypeScript compile error in the UI. That is the demo in the README GIF.
- The SDK generator became a critical path for the UI, which is why it landed in M1.

## Evidence

- `packages/pos-sdk/fixtures/consumer.ts`: type-checked consumer with an exhaustive error `switch` and `@ts-expect-error` checks for a missing idempotency key and a misspelled input field.
- `packages/sdk/src/runtime.test.ts`: transport behavior.
- `apps/pos-web/.oxlintrc.json`: the lint rule. Checked while writing this ADR by adding a probe file with `fetch(...)` and `window.fetch(...)`; both were reported with the ADR-0005 message.
- `apps/pos-web/src/lib/errors.ts`: the UI's exhaustive `switch` over `SalesCreateSaleErrorCode`.
- `apps/pos-web/src/lib/api.ts`: the only network code in the UI is `createHttpTransport` from the SDK, wrapped to record commands for the internals panel.
- `tests/e2e/checkout.spec.ts`: the UI drives real checkouts through the SDK into Postgres.
- Planned for M4: the README GIF of a contract change breaking a stale UI call site.
