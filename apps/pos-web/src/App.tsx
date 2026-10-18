import type { CatalogProduct } from '@modura/pos-sdk';
import { useMemo, useState } from 'react';
import { Cart, type CartLine, type CheckoutStatus } from './components/Cart';
import { Internals } from './components/Internals';
import { ProductGrid } from './components/ProductGrid';
import { Today } from './components/Today';
import { createClient, type CallRecord, type Role } from './lib/api';
import { useCatalog } from './lib/useCatalog';

interface CompletedCheckout {
  readonly key: string;
  readonly lines: readonly CartLine[];
}

export function App() {
  const [role, setRole] = useState<Role>('cashier');
  const [showInternals, setShowInternals] = useState(false);
  const [lastCommand, setLastCommand] = useState<CallRecord>();
  const client = useMemo(() => createClient(role, setLastCommand), [role]);
  const { products, stock, error, refreshStock } = useCatalog(client);

  const [lines, setLines] = useState<readonly CartLine[]>([]);
  // One key per checkout attempt. It survives retries of the same cart and is
  // dropped when the cart changes or the sale succeeds (ADR-0004).
  const [pendingKey, setPendingKey] = useState<string | null>(null);
  const [status, setStatus] = useState<CheckoutStatus>({ kind: 'idle' });
  const [completed, setCompleted] = useState<CompletedCheckout | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [retryNote, setRetryNote] = useState<string | null>(null);
  const [salesMade, setSalesMade] = useState(0);

  const productsById = useMemo(
    () => new Map<string, CatalogProduct>((products ?? []).map((p) => [p.id, p])),
    [products],
  );
  const inCart = Object.fromEntries(lines.map((l) => [l.productId, l.quantity]));

  function changeQuantity(productId: string, delta: number) {
    setLines((current) => {
      const existing = current.find((l) => l.productId === productId);
      const quantity = (existing?.quantity ?? 0) + delta;
      if (quantity <= 0) return current.filter((l) => l.productId !== productId);
      if (!existing) return [...current, { productId, quantity }];
      return current.map((l) => (l.productId === productId ? { ...l, quantity } : l));
    });
    setPendingKey(null);
    if (status.kind !== 'submitting') setStatus({ kind: 'idle' });
  }

  async function charge() {
    const key = pendingKey ?? crypto.randomUUID();
    setPendingKey(key);
    setStatus({ kind: 'submitting' });
    setRetryNote(null);

    const result = await client.sales.createSale({ lines: [...lines] }, { idempotencyKey: key });
    if (result.ok) {
      setStatus({ kind: 'success', sale: result.value, replayed: result.replayed });
      setCompleted({ key, lines });
      setLines([]);
      setPendingKey(null);
      if (!result.replayed) setSalesMade((n) => n + 1);
    } else {
      setStatus({ kind: 'error', error: result.error });
    }
    await refreshStock();
  }

  async function simulateRetry() {
    if (!completed) return;
    setRetrying(true);
    const before = { ...stock };
    const result = await client.sales.createSale(
      { lines: [...completed.lines] },
      { idempotencyKey: completed.key },
    );
    const after = await refreshStock();
    setRetrying(false);

    if (!result.ok) {
      setStatus({ kind: 'error', error: result.error });
      setRetryNote(null);
      return;
    }
    setStatus({ kind: 'success', sale: result.value, replayed: result.replayed });
    const moved = completed.lines.filter((l) => before[l.productId] !== after[l.productId]);
    setRetryNote(
      result.replayed && moved.length === 0
        ? `Replayed ${result.value.id}. Stock unchanged: ${completed.lines
            .map((l) => `${productsById.get(l.productId)?.name ?? l.productId} ${after[l.productId]}`)
            .join(', ')}.`
        : 'The server did not replay this checkout. That would be a bug.',
    );
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="logo" aria-hidden="true">M</span>
          <span>Modura POS</span>
        </div>
        <div className="controls">
          <div className="segmented" role="radiogroup" aria-label="Signed in as">
            {(['cashier', 'viewer'] as const).map((r) => (
              <button
                key={r}
                type="button"
                role="radio"
                aria-checked={role === r}
                className={role === r ? 'active' : ''}
                onClick={() => {
                  setRole(r);
                  setStatus({ kind: 'idle' });
                }}
              >
                {r === 'cashier' ? 'Cashier' : 'Viewer'}
              </button>
            ))}
          </div>
          <label className="toggle">
            <input
              type="checkbox"
              checked={showInternals}
              onChange={(e) => setShowInternals(e.target.checked)}
            />
            <span>Show internals</span>
          </label>
        </div>
      </header>

      <p className="demo-notice">
        Public demo with shared demo logins. Low stock refills automatically, and all data resets
        nightly at 03:00 UTC. {role === 'viewer' && 'Viewers can browse but not sell.'}
      </p>

      <main className="layout">
        {error ? (
          <p className="load-error" role="alert">
            Could not load the catalog: {error}
          </p>
        ) : products === null ? (
          <p className="loading">Loading catalog…</p>
        ) : (
          <ProductGrid
            products={products}
            stock={stock}
            inCart={inCart}
            onAdd={(id) => changeQuantity(id, 1)}
          />
        )}
        <Cart
          lines={lines}
          products={productsById}
          stock={stock}
          status={status}
          onChange={changeQuantity}
          onCharge={() => void charge()}
          onClear={() => {
            setLines([]);
            setPendingKey(null);
            setStatus({ kind: 'idle' });
          }}
        />
      </main>

      <Today client={client} products={productsById} salesMade={salesMade} />

      {showInternals && (
        <Internals
          pendingKey={pendingKey}
          last={lastCommand}
          retryNote={retryNote}
          canRetry={completed !== null}
          retrying={retrying}
          onSimulateRetry={() => void simulateRetry()}
        />
      )}
    </div>
  );
}
