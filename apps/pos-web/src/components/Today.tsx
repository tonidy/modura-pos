import type { CatalogProduct, ModuraClient, ReportsDaySummary } from '@modura/pos-sdk';
import { useCallback, useEffect, useState } from 'react';
import { formatMoney } from '../lib/money';

const POLL_MS = 5_000;

interface Props {
  readonly client: ModuraClient;
  readonly products: ReadonlyMap<string, CatalogProduct>;
  /** Changes after every sale, to refresh sooner than the next poll. */
  readonly salesMade: number;
}

/**
 * Today's sales from the reports service. Its numbers come from events, so they can
 * trail the till by a moment; `asOf` says how current they are. If the service is
 * down, only this panel notices.
 */
export function Today({ client, products, salesMade }: Props) {
  const [summary, setSummary] = useState<ReportsDaySummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    const result = await client.reports.dailySummary({});
    if (result.ok) {
      setSummary(result.value);
      setError(null);
    } else {
      setError(result.error.message);
    }
  }, [client]);

  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    if (salesMade === 0) return;
    // The event reaches the reports service a moment after the sale commits.
    const timer = setTimeout(() => void refresh(), 1_500);
    return () => clearTimeout(timer);
  }, [salesMade, refresh]);

  return (
    <section className="today" aria-labelledby="today-heading">
      <div className="today-header">
        <h2 id="today-heading">Today</h2>
        <button type="button" className="link" onClick={() => void refresh()}>
          Refresh
        </button>
      </div>
      {error ? (
        <p className="today-error" role="status">
          Reports are unavailable right now: {error} Selling still works.
        </p>
      ) : summary === null ? (
        <p className="loading">Loading today's sales…</p>
      ) : (
        <>
          <dl className="today-stats">
            <div>
              <dt>Sales</dt>
              <dd data-testid="today-sales">{summary.salesCount}</dd>
            </div>
            <div>
              <dt>Items</dt>
              <dd>{summary.itemsSold}</dd>
            </div>
            <div>
              <dt>Revenue</dt>
              <dd data-testid="today-revenue">
                {summary.revenue.length === 0 ? formatMoney({ amountMinor: 0, currency: 'USD' }) : summary.revenue.map(formatMoney).join(' + ')}
              </dd>
            </div>
          </dl>
          {summary.topProducts.length > 0 && (
            <ol className="today-top">
              {summary.topProducts.map((p) => (
                <li key={p.productId}>
                  <span>{products.get(p.productId)?.name ?? p.productId}</span>
                  <span>
                    {p.quantity} sold, {formatMoney(p.revenue)}
                  </span>
                </li>
              ))}
            </ol>
          )}
          <p className="today-asof">
            {summary.asOf
              ? `Includes sales up to ${new Date(summary.asOf).toLocaleTimeString()}, from the reports service.`
              : 'No sales processed yet.'}
          </p>
        </>
      )}
    </section>
  );
}
