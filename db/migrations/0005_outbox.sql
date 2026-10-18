-- Transactional outbox (plan M7, ADR-0011). Events are written in the same
-- transaction as the change they describe, so an event exists exactly when its
-- change committed. Consumers read this table; they never read sales tables.

CREATE TABLE outbox (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  event_id    uuid NOT NULL UNIQUE,
  -- Qualified event name, for example 'sales.saleCompleted'.
  event       text NOT NULL CHECK (event ~ '^[a-z][a-zA-Z0-9]*\.[a-z][a-zA-Z0-9]*$'),
  payload     jsonb NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  -- The writing transaction. Consumers only read rows whose transaction is older
  -- than every transaction still running (pg_snapshot_xmin), which is what keeps a
  -- late-committing row with a lower id from being skipped.
  tx          xid8 NOT NULL DEFAULT pg_current_xact_id()
);

CREATE INDEX outbox_tx_idx ON outbox (tx, id);

-- Append-only, like contract releases: consumers keep their own position.
CREATE FUNCTION append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER outbox_append_only
  BEFORE UPDATE OR DELETE ON outbox
  FOR EACH ROW EXECUTE FUNCTION append_only();
