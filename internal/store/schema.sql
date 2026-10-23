PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','member')),
  password_hash TEXT,
  totp_secret_enc TEXT,
  totp_confirmed INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  last_login_at TEXT
);

CREATE TABLE IF NOT EXISTS user_configs (
  user_id TEXT PRIMARY KEY REFERENCES users(id),
  payload TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schedules (
  user_id TEXT PRIMARY KEY REFERENCES users(id),
  enabled INTEGER NOT NULL DEFAULT 0,
  local_time TEXT NOT NULL DEFAULT '20:00',
  timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  next_run_at TEXT,
  last_run_at TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_sessions (
  id_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  csrf_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  idle_expires_at TEXT NOT NULL,
  revoked_at TEXT,
  ip_hint TEXT
);
CREATE INDEX IF NOT EXISTS ix_auth_sessions_user_id ON auth_sessions(user_id);

CREATE TABLE IF NOT EXISTS api_tokens (
  id_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  label TEXT NOT NULL,
  kind TEXT NOT NULL,
  scopes TEXT NOT NULL,
  run_id TEXT,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  expires_at TEXT,
  revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS ix_api_tokens_user_id ON api_tokens(user_id);

CREATE TABLE IF NOT EXISTS invites (
  id TEXT PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  username TEXT NOT NULL,
  display_name TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','member')),
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT
);

CREATE TABLE IF NOT EXISTS bili_credentials (
  user_id TEXT PRIMARY KEY REFERENCES users(id),
  ciphertext TEXT NOT NULL,
  uid INTEGER,
  nickname TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  kind TEXT NOT NULL,
  runner TEXT NOT NULL,
  status TEXT NOT NULL,
  scheduled_for TEXT,
  started_at TEXT,
  heartbeat_at TEXT,
  finished_at TEXT,
  error TEXT,
  picked_count INTEGER NOT NULL DEFAULT 0,
  telemetry TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS ix_runs_user_id ON runs(user_id);
CREATE INDEX IF NOT EXISTS ix_runs_status ON runs(status);
CREATE INDEX IF NOT EXISTS ix_runs_user_status ON runs(user_id,status);
CREATE UNIQUE INDEX IF NOT EXISTS uq_runs_active_user
  ON runs(user_id) WHERE status IN ('queued','running');

CREATE TABLE IF NOT EXISTS run_candidates (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  user_id TEXT NOT NULL REFERENCES users(id),
  bvid TEXT NOT NULL,
  payload TEXT NOT NULL,
  seen_at TEXT NOT NULL,
  UNIQUE(run_id,bvid)
);
CREATE INDEX IF NOT EXISTS ix_run_candidates_run_id ON run_candidates(run_id);
CREATE INDEX IF NOT EXISTS ix_run_candidates_user_id ON run_candidates(user_id);
CREATE INDEX IF NOT EXISTS ix_run_candidates_bvid ON run_candidates(bvid);

CREATE TABLE IF NOT EXISTS video_records (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  bvid TEXT NOT NULL,
  title TEXT NOT NULL,
  author TEXT NOT NULL,
  url TEXT NOT NULL,
  pubdate TEXT,
  first_run_id TEXT REFERENCES runs(id),
  state TEXT NOT NULL,
  tier TEXT,
  reason TEXT,
  one_liner TEXT,
  day TEXT NOT NULL,
  delivered INTEGER NOT NULL DEFAULT 0,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  seen_at TEXT NOT NULL,
  UNIQUE(user_id,bvid)
);
CREATE INDEX IF NOT EXISTS ix_video_records_user_id ON video_records(user_id);
CREATE INDEX IF NOT EXISTS ix_video_records_day ON video_records(day);
CREATE INDEX IF NOT EXISTS ix_video_records_state ON video_records(state);
CREATE INDEX IF NOT EXISTS ix_video_records_bvid ON video_records(bvid);

CREATE TABLE IF NOT EXISTS reports (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  day TEXT NOT NULL,
  markdown TEXT NOT NULL,
  summary TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id,day)
);
CREATE INDEX IF NOT EXISTS ix_reports_user_id ON reports(user_id);
CREATE INDEX IF NOT EXISTS ix_reports_day ON reports(day);

CREATE TABLE IF NOT EXISTS deliveries (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  run_id TEXT NOT NULL REFERENCES runs(id),
  bvid TEXT NOT NULL,
  kind TEXT NOT NULL,
  content TEXT NOT NULL,
  status TEXT NOT NULL,
  remote_id TEXT,
  root_id TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  created_at TEXT NOT NULL,
  sent_at TEXT,
  verified_at TEXT,
  visible INTEGER,
  UNIQUE(user_id,run_id,bvid,kind,root_id)
);
CREATE INDEX IF NOT EXISTS ix_deliveries_user_id ON deliveries(user_id);
CREATE INDEX IF NOT EXISTS ix_deliveries_run_id ON deliveries(run_id);
CREATE INDEX IF NOT EXISTS ix_deliveries_kind ON deliveries(kind);
CREATE INDEX IF NOT EXISTS ix_deliveries_status ON deliveries(status);

CREATE TABLE IF NOT EXISTS dm_cursors (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  talker_id INTEGER NOT NULL,
  last_message_at INTEGER NOT NULL,
  last_message_id TEXT,
  updated_at TEXT NOT NULL,
  UNIQUE(user_id,talker_id)
);
CREATE INDEX IF NOT EXISTS ix_dm_cursors_user_id ON dm_cursors(user_id);
CREATE INDEX IF NOT EXISTS ix_dm_cursors_talker_id ON dm_cursors(talker_id);

CREATE TABLE IF NOT EXISTS provider_accounts (
  provider_id TEXT PRIMARY KEY,
  base_url TEXT NOT NULL,
  api_key_enc TEXT,
  low_balance_threshold REAL NOT NULL DEFAULT 10,
  enabled INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS balance_snapshots (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  provider_id TEXT NOT NULL,
  currency TEXT NOT NULL,
  total_balance REAL NOT NULL,
  granted_balance REAL NOT NULL,
  topped_up_balance REAL NOT NULL,
  is_available INTEGER NOT NULL,
  low_balance INTEGER NOT NULL,
  source TEXT NOT NULL,
  fetched_at TEXT NOT NULL,
  UNIQUE(user_id,provider_id,currency,fetched_at)
);
CREATE INDEX IF NOT EXISTS ix_balance_snapshots_user_id ON balance_snapshots(user_id);

CREATE TABLE IF NOT EXISTS usage_records (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  source TEXT NOT NULL,
  app_type TEXT NOT NULL,
  date TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  model TEXT NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  total_cost_usd REAL NOT NULL DEFAULT 0,
  avg_latency_ms REAL NOT NULL DEFAULT 0,
  imported_at TEXT NOT NULL,
  UNIQUE(user_id,source,date,app_type,provider_id,model)
);
CREATE INDEX IF NOT EXISTS ix_usage_records_user_id ON usage_records(user_id);
CREATE INDEX IF NOT EXISTS ix_usage_records_date ON usage_records(date);

CREATE TABLE IF NOT EXISTS system_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  user_id TEXT,
  actor_user_id TEXT,
  event TEXT NOT NULL,
  target TEXT,
  detail TEXT NOT NULL DEFAULT '{}',
  ip TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_audit_events_user_id ON audit_events(user_id);
CREATE INDEX IF NOT EXISTS ix_audit_events_actor_user_id ON audit_events(actor_user_id);
CREATE INDEX IF NOT EXISTS ix_audit_events_event ON audit_events(event);
