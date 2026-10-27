-- Forward-only; never edit an applied file.
--
-- Per-run token/cost telemetry from the runner. usage_records stays a per-day
-- rollup fed by cc-switch imports; mixing the two would double count.

CREATE TABLE IF NOT EXISTS run_usage (
  run_id TEXT PRIMARY KEY REFERENCES runs(id),
  user_id TEXT NOT NULL REFERENCES users(id),
  runner TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  source TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd REAL NOT NULL DEFAULT 0,
  request_count INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_run_usage_user_id ON run_usage(user_id);
