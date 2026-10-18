-- Sales, stock movements and idempotency records for sales.createSale.
-- See docs/adr/0004-create-sale-idempotency.md and docs/adr/0006-money.md.

CREATE TABLE sales (
  id                text PRIMARY KEY CHECK (id ~ '^sale_[0-9a-z]{1,64}$'),
  idempotency_scope text NOT NULL,
  idempotency_key   uuid NOT NULL,
  total_minor       bigint NOT NULL CHECK (total_minor >= 0),
  currency          text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (idempotency_scope, idempotency_key)
);

-- Prices are snapshotted here, so later catalog changes never alter a past sale.
CREATE TABLE sale_lines (
  sale_id          text NOT NULL REFERENCES sales (id),
  line_no          integer NOT NULL CHECK (line_no >= 1),
  product_id       text NOT NULL REFERENCES products (id),
  quantity         integer NOT NULL CHECK (quantity BETWEEN 1 AND 999),
  unit_price_minor bigint NOT NULL CHECK (unit_price_minor >= 0),
  line_total_minor bigint NOT NULL,
  PRIMARY KEY (sale_id, line_no),
  UNIQUE (sale_id, product_id),
  CHECK (line_total_minor = unit_price_minor * quantity)
);

-- Every stock change is recorded, so inventory.available can be audited.
CREATE TABLE stock_movements (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  product_id text NOT NULL REFERENCES products (id),
  delta      integer NOT NULL CHECK (delta <> 0),
  reason     text NOT NULL CHECK (reason IN ('sale', 'restock')),
  sale_id    text REFERENCES sales (id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((reason = 'sale') = (sale_id IS NOT NULL))
);

CREATE INDEX stock_movements_product_idx ON stock_movements (product_id, created_at);

-- The row is inserted at the start of the checkout transaction (claim) and completed
-- just before commit, so other sessions only ever see completed rows. A concurrent
-- insert of the same (scope, key) waits on this primary key until the owner finishes.
CREATE TABLE idempotency_keys (
  scope           text NOT NULL,
  key             uuid NOT NULL,
  operation       text NOT NULL,
  fingerprint     bytea NOT NULL CHECK (length(fingerprint) = 32),
  response_status integer,
  response_body   jsonb,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (scope, key)
);
