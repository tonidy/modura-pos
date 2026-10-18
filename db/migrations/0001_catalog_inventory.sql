-- Catalog and inventory. Sales, stock movements and idempotency arrive in M2.

CREATE TABLE products (
  id          text PRIMARY KEY CHECK (id ~ '^prd_[a-z0-9_]{1,60}$'),
  sku         text NOT NULL UNIQUE CHECK (length(sku) BETWEEN 1 AND 32),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  category    text NOT NULL CHECK (length(category) BETWEEN 1 AND 60),
  -- Money is integer minor units plus an ISO 4217 code. See docs/adr/0006-money.md.
  price_minor bigint NOT NULL CHECK (price_minor >= 0),
  currency    text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  active      boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE inventory (
  product_id  text PRIMARY KEY REFERENCES products (id),
  -- Safety net only. Checkout logic must never rely on hitting this constraint.
  available   integer NOT NULL CHECK (available >= 0),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
