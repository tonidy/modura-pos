import type { CatalogProduct, ModuraError, SalesCreateSaleErrorCode, SalesSale } from '@modura/pos-sdk';
import { explainCheckoutError } from '../lib/errors';
import { formatMoney, multiply } from '../lib/money';

export interface CartLine {
  readonly productId: string;
  readonly quantity: number;
}

export type CheckoutStatus =
  | { readonly kind: 'idle' }
  | { readonly kind: 'submitting' }
  | { readonly kind: 'success'; readonly sale: SalesSale; readonly replayed: boolean }
  | { readonly kind: 'error'; readonly error: ModuraError<SalesCreateSaleErrorCode> };

interface Props {
  readonly lines: readonly CartLine[];
  readonly products: ReadonlyMap<string, CatalogProduct>;
  readonly stock: Readonly<Record<string, number>>;
  readonly status: CheckoutStatus;
  readonly onChange: (productId: string, delta: number) => void;
  readonly onCharge: () => void;
  readonly onClear: () => void;
}

export function Cart({ lines, products, stock, status, onChange, onCharge, onClear }: Props) {
  const priced = lines.flatMap((line) => {
    const product = products.get(line.productId);
    return product ? [{ ...line, product }] : [];
  });
  const total = priced.reduce(
    (sum, line) => sum + line.product.price.amountMinor * line.quantity,
    0,
  );
  const currency = priced[0]?.product.price.currency ?? 'USD';
  const submitting = status.kind === 'submitting';

  return (
    <aside className="cart" aria-labelledby="cart-heading">
      <div className="cart-header">
        <h2 id="cart-heading">Current order</h2>
        {priced.length > 0 && (
          <button type="button" className="link" onClick={onClear} disabled={submitting}>
            Clear
          </button>
        )}
      </div>

      {priced.length === 0 ? (
        <p className="empty">Tap a product to start an order.</p>
      ) : (
        <ul className="lines">
          {priced.map(({ productId, quantity, product }) => {
            const available = stock[productId];
            const short = available !== undefined && quantity > available;
            return (
              <li key={productId} className="line">
                <div className="line-main">
                  <span className="line-name">{product.name}</span>
                  <span className="line-total">{formatMoney(multiply(product.price, quantity))}</span>
                </div>
                <div className="line-sub">
                  <div className="stepper" role="group" aria-label={`${product.name} quantity`}>
                    <button type="button" onClick={() => onChange(productId, -1)} aria-label={`One less ${product.name}`} disabled={submitting}>
                      −
                    </button>
                    <span aria-live="polite">{quantity}</span>
                    <button type="button" onClick={() => onChange(productId, 1)} aria-label={`One more ${product.name}`} disabled={submitting}>
                      +
                    </button>
                  </div>
                  {short && <span className="warn">Only {available} in stock</span>}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <div className="cart-footer">
        <div className="total">
          <span>Total</span>
          <span>{formatMoney({ amountMinor: total, currency })}</span>
        </div>
        <button
          type="button"
          className="charge"
          onClick={onCharge}
          disabled={priced.length === 0 || submitting}
        >
          {submitting
            ? 'Charging…'
            : priced.length === 0
              ? 'Charge'
              : `Charge ${formatMoney({ amountMinor: total, currency })}`}
        </button>
        <p className="price-note">Prices are confirmed by the server at checkout.</p>
      </div>

      <CheckoutResult status={status} products={products} />
    </aside>
  );
}

function CheckoutResult({
  status,
  products,
}: {
  readonly status: CheckoutStatus;
  readonly products: ReadonlyMap<string, CatalogProduct>;
}) {
  if (status.kind === 'success') {
    const { sale, replayed } = status;
    return (
      <div className="result success" role="status" aria-live="polite">
        <div className="result-title">
          <span>Sale complete</span>
          {replayed && <span className="tag">Replayed</span>}
        </div>
        <ul className="receipt">
          {sale.lines.map((line) => (
            <li key={line.productId}>
              <span>
                {line.quantity} × {products.get(line.productId)?.name ?? line.productId}
              </span>
              <span>{formatMoney(line.lineTotal)}</span>
            </li>
          ))}
        </ul>
        <div className="receipt-total">
          <span>Paid</span>
          <span>{formatMoney(sale.total)}</span>
        </div>
        <code className="sale-id">{sale.id}</code>
      </div>
    );
  }
  if (status.kind === 'error') {
    return (
      <div className="result error" role="alert">
        <div className="result-title">{explainCheckoutError(status.error.code)}</div>
        <p>{status.error.message}</p>
        <code>{status.error.code}</code>
      </div>
    );
  }
  return null;
}
