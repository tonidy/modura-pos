import type { SalesCreateSaleErrorCode } from '@modura/pos-sdk';

/**
 * Cashier-facing text for every error checkout can return. The switch is exhaustive
 * on purpose: when the contract adds a code, this stops compiling (ADR-0005).
 */
export function explainCheckoutError(code: SalesCreateSaleErrorCode): string {
  switch (code) {
    case 'sales.INSUFFICIENT_STOCK':
      return 'Not enough stock for this order. Nothing was sold.';
    case 'sales.UNKNOWN_PRODUCT':
    case 'sales.PRODUCT_NOT_SELLABLE':
      return 'One of these products can no longer be sold. Remove it and try again.';
    case 'FORBIDDEN':
      return 'This role cannot sell. Switch to Cashier.';
    case 'UNAUTHENTICATED':
      return 'The register is not signed in.';
    case 'RATE_LIMITED':
      return 'Too many checkouts from this register. Wait a moment, then charge again.';
    case 'IDEMPOTENCY_IN_PROGRESS':
      return 'The previous attempt is still processing. Charge again to retry it safely.';
    case 'UNAVAILABLE':
      return 'The server is unreachable. Charge again to retry; you will not be charged twice.';
    case 'IDEMPOTENCY_KEY_REUSED':
    case 'VALIDATION_FAILED':
    case 'INTERNAL':
      return 'Something went wrong on our side. The sale did not go through.';
    default:
      return assertNever(code);
  }
}

function assertNever(value: never): never {
  throw new Error(`Unhandled error code: ${String(value)}`);
}
