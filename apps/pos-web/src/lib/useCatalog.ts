import type { CatalogProduct, ModuraClient } from '@modura/pos-sdk';
import { useCallback, useEffect, useRef, useState } from 'react';

export interface Catalog {
  readonly products: readonly CatalogProduct[] | null;
  /** Available units by product ID, straight from inventory.getStock. */
  readonly stock: Readonly<Record<string, number>>;
  readonly error: string | null;
  readonly refreshStock: () => Promise<Record<string, number>>;
}

export function useCatalog(client: ModuraClient): Catalog {
  const [products, setProducts] = useState<readonly CatalogProduct[] | null>(null);
  const [stock, setStock] = useState<Record<string, number>>({});
  const [error, setError] = useState<string | null>(null);
  const productsRef = useRef<readonly CatalogProduct[]>([]);

  const refreshStock = useCallback(async () => {
    const results = await Promise.all(
      productsRef.current.map(async (product) => {
        const result = await client.inventory.getStock({ productId: product.id });
        return [product.id, result.ok ? result.value.available : undefined] as const;
      }),
    );
    const next: Record<string, number> = {};
    for (const [id, available] of results) if (available !== undefined) next[id] = available;
    setStock(next);
    return next;
  }, [client]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const result = await client.catalog.listProducts({});
      if (cancelled) return;
      if (!result.ok) {
        setError(result.error.message);
        return;
      }
      productsRef.current = result.value.products;
      setProducts(result.value.products);
      setError(null);
      await refreshStock();
    })();
    return () => {
      cancelled = true;
    };
  }, [client, refreshStock]);

  return { products, stock, error, refreshStock };
}
