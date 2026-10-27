-- Forward-only; never edit an applied file.
--
-- Deliveries become jobs: finish_run inserts rows as 'pending' inside its commit
-- transaction and a background worker claims them with a CAS update, so a slow
-- Bilibili call can never be aborted by the MCP request timeout.

ALTER TABLE deliveries ADD COLUMN payload TEXT NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS ix_deliveries_pending ON deliveries(status, created_at)
  WHERE status = 'pending';
