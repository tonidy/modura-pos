import type { CatalogProduct } from '@modura/pos-sdk';
import { formatMoney } from '../lib/money';

interface Props {
  readonly products: readonly CatalogProduct[];
  readonly stock: Readonly<Record<string, number>>;
  readonly inCart: Readonly<Record<string, number>>;
  readonly onAdd: (productId: string) => void;
}

export function ProductGrid({ products, stock, inCart, onAdd }: Props) {
  // The server already orders by category, then name.
  const categories = [...new Set(products.map((p) => p.category))];

  return (
    <section className="products" aria-labelledby="products-heading">
      <h2 id="products-heading" className="visually-hidden">
        Products
      </h2>
      {categories.map((category) => (
        <div key={category} className="category">
          <h3>{category}</h3>
          <ul>
            {products
              .filter((p) => p.category === category)
              .map((product) => {
                const available = stock[product.id];
                const soldOut = available === 0;
                return (
                  <li key={product.id}>
                    <button
                      type="button"
                      className="product"
                      disabled={soldOut}
                      onClick={() => onAdd(product.id)}
                      aria-label={`Add ${product.name}, ${formatMoney(product.price)}`}
                    >
                      <span className="product-name">{product.name}</span>
                      <span className="product-price">{formatMoney(product.price)}</span>
                      <StockBadge available={available} />
                      {inCart[product.id] ? (
                        <span className="in-cart" aria-label={`${inCart[product.id]} in cart`}>
                          {inCart[product.id]}
                        </span>
                      ) : null}
                    </button>
                  </li>
                );
              })}
          </ul>
        </div>
      ))}
    </section>
  );
}

function StockBadge({ available }: { readonly available: number | undefined }) {
  if (available === undefined) return <span className="stock">checking stock</span>;
  if (available === 0) return <span className="stock out">Sold out</span>;
  if (available <= 3) return <span className="stock low">{available} left</span>;
  return <span className="stock">{available} in stock</span>;
}
