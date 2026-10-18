-- Contract Registry releases (plan M5, ADR-0008). Append-only: a release is never
-- updated or deleted, so a number always means the same contract.

CREATE TABLE contract_releases (
  number       integer PRIMARY KEY CHECK (number >= 1),
  -- Canonical compact JSON, stored as `json` (not `jsonb`) so the exact bytes that
  -- were hashed come back out.
  ir           json NOT NULL,
  ir_sha256    text NOT NULL CHECK (ir_sha256 ~ '^[0-9a-f]{64}$'),
  ir_version   text NOT NULL,
  modules      jsonb NOT NULL,
  changes      jsonb NOT NULL,
  published_by text NOT NULL,
  published_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION contract_releases_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'contract_releases is append-only';
END;
$$;

CREATE TRIGGER contract_releases_append_only
  BEFORE UPDATE OR DELETE ON contract_releases
  FOR EACH ROW EXECUTE FUNCTION contract_releases_append_only();
