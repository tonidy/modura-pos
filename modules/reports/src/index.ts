import { ProductId } from '@modura/module-catalog';
import { defineModule, defineType, t } from '@modura/schema';

// Sales reporting, owned by the Python service (plan M7, ADR-0011). Its data comes
// from the sales.saleCompleted event through a transactional outbox, so every answer
// says how current it is (`asOf`).

export const ReportDate = defineType(
  'reports.ReportDate',
  t.string({ pattern: '^[0-9]{4}-[0-9]{2}-[0-9]{2}$', description: 'A calendar date in UTC, YYYY-MM-DD.' }),
);

export const ProductSales = defineType(
  'reports.ProductSales',
  t.object({
    productId: ProductId,
    quantity: t.integer({ minimum: 0 }),
    revenue: t.money(),
  }),
);

const asOf = t.optional(
  t.timestamp({
    description: 'When the most recent sale this service has processed happened. Missing until the first one arrives.',
  }),
);

// Not "reports.DailySummary": that would generate the same identifier as the
// operation reports.dailySummary (the compiler refuses it, ADR-0010).
export const DaySummary = defineType(
  'reports.DaySummary',
  t.object({
    date: ReportDate,
    salesCount: t.integer({ minimum: 0 }),
    itemsSold: t.integer({ minimum: 0 }),
    revenue: t.array(t.money(), { description: 'One total per currency; empty when nothing was sold.' }),
    topProducts: t.array(ProductSales, { maxItems: 5 }),
    asOf,
  }),
);

export const reports = defineModule({
  name: 'reports',
  version: '0.1.0',
  description: 'Sales reports built from completed sales, eventually consistent.',
  permissions: {
    read: 'Read sales reports.',
  },
  errors: {
    INVALID_RANGE: {
      category: 'unprocessable',
      retryable: false,
      description: 'The date range is reversed or longer than 92 days.',
    },
  },
  operations: {
    dailySummary: {
      kind: 'query',
      description: 'Sales count, items sold, revenue and the top five products for one day (UTC), today by default.',
      requires: ['reports.read'],
      input: t.object({ date: t.optional(ReportDate) }),
      output: DaySummary,
      expose: { mcp: true },
    },
    topProducts: {
      kind: 'query',
      description: 'Best-selling products over a date range (UTC, inclusive), by units sold.',
      requires: ['reports.read'],
      input: t.object({
        from: ReportDate,
        to: ReportDate,
        limit: t.optional(t.integer({ minimum: 1, maximum: 20 })),
      }),
      output: t.object({ from: ReportDate, to: ReportDate, products: t.array(ProductSales, { maxItems: 20 }), asOf }),
      errors: ['INVALID_RANGE'],
      expose: { mcp: true },
    },
  },
  subscribes: ['sales.saleCompleted'],
});
