-- Events the consumer cannot apply however often it tries: a payload that fails the
-- event model, or one the read model's constraints reject. Each is recorded here and
-- claimed in the inbox, so it no longer blocks the events after it.
--
-- To replay one after a fix: delete its rows here and in reports.inbox, and set
-- reports.consumer_state.horizon to at most its tx.
CREATE TABLE reports.dead_letters (
  event_id  uuid PRIMARY KEY,
  event     text NOT NULL,
  tx        xid8 NOT NULL,
  payload   jsonb NOT NULL,
  error     text NOT NULL,
  failed_at timestamptz NOT NULL DEFAULT now()
);
