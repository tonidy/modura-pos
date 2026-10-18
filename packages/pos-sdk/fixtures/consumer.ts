/**
 * Type-checked consumer of the generated SDK (M1 exit criterion). It is never run.
 * If a contract change breaks one of these call sites, `pnpm verify` fails here.
 */
import {
  createHttpTransport,
  createModuraClient,
  type Money,
  type SalesCreateSaleErrorCode,
} from '../src';

const client = createModuraClient(
  createHttpTransport({ baseUrl: 'http://localhost:8080', token: 'demo-cashier' }),
);

export async function checkout(): Promise<string> {
  const result = await client.sales.createSale(
    { lines: [{ productId: 'prd_croissant', quantity: 2 }] },
    { idempotencyKey: crypto.randomUUID() },
  );
  if (result.ok) return formatMoney(result.value.total);
  return explain(result.error.code);
}

export async function stockOf(productId: string): Promise<number | 'unknown'> {
  const result = await client.inventory.getStock({ productId });
  if (result.ok) return result.value.available;
  return result.error.code === 'inventory.PRODUCT_NOT_FOUND' ? 'unknown' : 0;
}

function formatMoney(money: Money): string {
  return `${(money.amountMinor / 100).toFixed(2)} ${money.currency}`;
}

/** Exhaustive on purpose: a new error code in the contract is a compile error here. */
function explain(code: SalesCreateSaleErrorCode): string {
  switch (code) {
    case 'sales.INSUFFICIENT_STOCK':
      return 'Not enough stock for this order.';
    case 'sales.UNKNOWN_PRODUCT':
    case 'sales.PRODUCT_NOT_SELLABLE':
      return 'One of these products can no longer be sold.';
    case 'IDEMPOTENCY_IN_PROGRESS':
    case 'RATE_LIMITED':
    case 'UNAVAILABLE':
      return 'Busy, try again.';
    case 'IDEMPOTENCY_KEY_REUSED':
    case 'VALIDATION_FAILED':
    case 'INTERNAL':
      return 'Something went wrong.';
    case 'UNAUTHENTICATED':
    case 'FORBIDDEN':
      return 'Sign in as a cashier.';
    default:
      return assertNever(code);
  }
}

function assertNever(value: never): never {
  throw new Error(`Unhandled case: ${String(value)}`);
}

export function misuse(): void {
  // @ts-expect-error idempotent operations require an idempotency key
  void client.sales.createSale({ lines: [{ productId: 'prd_espresso', quantity: 1 }] });

  // @ts-expect-error inputs are typed; the field is productId
  void client.inventory.getStock({ productID: 'prd_espresso' });
}
