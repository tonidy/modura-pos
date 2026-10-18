import { defineModule, defineType, t } from '@modura/schema';

export const ProductId = defineType(
  'catalog.ProductId',
  t.string({
    pattern: '^prd_[a-z0-9_]{1,60}$',
    description: 'Stable product identifier, for example "prd_flat_white".',
  }),
);

export const Product = defineType(
  'catalog.Product',
  t.object(
    {
      id: ProductId,
      sku: t.string({ minLength: 1, maxLength: 32 }),
      name: t.string({ minLength: 1, maxLength: 120 }),
      category: t.string({ minLength: 1, maxLength: 60 }),
      price: t.money({ description: 'Current unit price.' }),
    },
    { description: 'A product the cashier can sell.' },
  ),
);

export const catalog = defineModule({
  name: 'catalog',
  version: '0.1.0',
  description: 'Sellable products and their current prices.',
  permissions: {
    read: 'Read the product catalog.',
  },
  operations: {
    listProducts: {
      kind: 'query',
      description:
        'List active, sellable products with their current prices. Inactive products are omitted.',
      requires: ['catalog.read'],
      input: t.object({}),
      output: t.object({ products: t.array(Product) }),
      expose: { mcp: true },
    },
  },
});
