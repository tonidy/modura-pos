import { ProductId } from '@modura/module-catalog';
import { defineModule, defineType, t } from '@modura/schema';

export const SaleId = defineType(
  'sales.SaleId',
  t.string({
    pattern: '^sale_[0-9a-z]{1,64}$',
    description: 'Server-generated sale identifier.',
  }),
);

export const SaleLineInput = defineType(
  'sales.SaleLineInput',
  t.object({
    productId: ProductId,
    quantity: t.integer({ minimum: 1, maximum: 999 }),
  }),
);

export const SaleLine = defineType(
  'sales.SaleLine',
  t.object({
    productId: ProductId,
    quantity: t.integer({ minimum: 1, maximum: 999 }),
    unitPrice: t.money({ description: 'Catalog price snapshotted when the sale was created.' }),
    lineTotal: t.money({ description: 'unitPrice multiplied by quantity.' }),
  }),
);

export const Sale = defineType(
  'sales.Sale',
  t.object({
    id: SaleId,
    lines: t.array(SaleLine, { minItems: 1 }),
    total: t.money(),
    createdAt: t.timestamp(),
  }),
);

export const sales = defineModule({
  name: 'sales',
  // 0.2.0: the saleCompleted event (plan M7).
  version: '0.2.0',
  description: 'Checkout at the register.',
  events: {
    saleCompleted: {
      description:
        'A sale was committed. Written to the outbox in the same transaction as the sale, delivered at least once.',
      payload: Sale,
    },
  },
  permissions: {
    write: 'Create sales at the register.',
  },
  errors: {
    UNKNOWN_PRODUCT: {
      category: 'unprocessable',
      retryable: false,
      description: 'A line refers to a product that does not exist.',
    },
    PRODUCT_NOT_SELLABLE: {
      category: 'unprocessable',
      retryable: false,
      description: 'A line refers to a product that is no longer active.',
    },
    INSUFFICIENT_STOCK: {
      category: 'unprocessable',
      retryable: false,
      description: 'At least one line asks for more units than are available. Nothing was sold.',
    },
  },
  operations: {
    createSale: {
      kind: 'command',
      description:
        'Sell the given products atomically. Prices come from the catalog, never from the client. ' +
        'Lines with the same product are merged before processing.',
      requires: ['sales.write'],
      idempotent: true,
      input: t.object({
        lines: t.array(SaleLineInput, { minItems: 1, maxItems: 50 }),
      }),
      output: Sale,
      errors: ['UNKNOWN_PRODUCT', 'PRODUCT_NOT_SELLABLE', 'INSUFFICIENT_STOCK'],
    },
  },
});
