-- The Contract Registry moved to its own service, apps/registry-go, which keeps its
-- data in its own "registry" schema (plan M9, ADR-0013). The POS no longer serves
-- the registry, so its table goes. Earlier migrations are never rewritten, so
-- 0004_contract_releases.sql stays as it was.
--
-- Releases published to a POS database before this migration are not carried over:
-- the history that matters lives in git (file registry, ADR-0012), and the standalone
-- service starts with an empty history. The trigger and its function go with the table.

DROP TABLE contract_releases;
DROP FUNCTION contract_releases_append_only();
