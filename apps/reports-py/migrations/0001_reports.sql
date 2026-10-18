-- The reports service's own schema (plan M7, ADR-0011). It reads public.outbox and
-- nothing else owned by the Go service, and nothing outside this schema writes here.

CREATE TABLE reports.inbox (
  event_id     uuid PRIMARY KEY,
  processed_at timestamptz NOT NULL DEFAULT now()
);

-- How far the consumer has read: every outbox row whose writing transaction is older
-- than `horizon` has been seen. last_event_at is what responses report as asOf.
CREATE TABLE reports.consumer_state (
  consumer      text PRIMARY KEY,
  horizon       xid8 NOT NULL,
  last_event_at timestamptz
);

CREATE TABLE reports.sales (
  sale_id     text PRIMARY KEY,
  sold_at     timestamptz NOT NULL,
  items       integer NOT NULL CHECK (items > 0),
  total_minor bigint NOT NULL CHECK (total_minor >= 0),
  currency    text NOT NULL
);

CREATE INDEX sales_sold_at_idx ON reports.sales (sold_at);

CREATE TABLE reports.sale_lines (
  sale_id          text NOT NULL REFERENCES reports.sales (sale_id),
  product_id       text NOT NULL,
  quantity         integer NOT NULL CHECK (quantity > 0),
  line_total_minor bigint NOT NULL,
  currency         text NOT NULL,
  PRIMARY KEY (sale_id, product_id)
);
