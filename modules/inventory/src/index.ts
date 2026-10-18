import { ProductId } from '@modura/module-catalog';
import { defineModule, defineType, t } from '@modura/schema';

export const StockLevel = defineType(
  'inventory.StockLevel',
  t.object({
    productId: ProductId,
    available: t.integer({ minimum: 0, description: 'Units available to sell right now.' }),
    updatedAt: t.timestamp({ description: 'When this stock level last changed.' }),
  }),
);

export const inventory = defineModule({
  name: 'inventory',
  version: '0.1.0',
  description: 'Authoritative stock levels for sellable products.',
  permissions: {
    read: 'Read stock levels.',
  },
  errors: {
    PRODUCT_NOT_FOUND: {
      category: 'not_found',
      retryable: false,
      description:
        'No product exists with this identifier. A known product with no stock returns available: 0 instead.',
    },
  },
  operations: {
    getStock: {
      kind: 'query',
      description: 'Return the authoritative available stock for one product.',
      requires: ['inventory.read'],
      input: t.object({ productId: ProductId }),
      output: StockLevel,
      errors: ['PRODUCT_NOT_FOUND'],
      expose: { mcp: true },
    },
  },
});
