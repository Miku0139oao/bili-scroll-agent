-- Forward-only; never edit an applied file.
--
-- Turns provider_accounts from a DeepSeek-shaped row into a generic registry of
-- provider adapters. Existing provider_id='deepseek' rows keep their encrypted
-- key (AAD "provider:deepseek") and are backfilled with display metadata.

ALTER TABLE provider_accounts ADD COLUMN kind TEXT NOT NULL DEFAULT 'deepseek';
ALTER TABLE provider_accounts ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_accounts ADD COLUMN auth_style TEXT NOT NULL DEFAULT 'bearer';
ALTER TABLE provider_accounts ADD COLUMN currency TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_accounts ADD COLUMN script TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_accounts ADD COLUMN script_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE provider_accounts ADD COLUMN allow_hosts TEXT NOT NULL DEFAULT '[]';
ALTER TABLE provider_accounts ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';
ALTER TABLE provider_accounts ADD COLUMN created_at TEXT NOT NULL DEFAULT '';

UPDATE provider_accounts
   SET display_name = 'DeepSeek',
       currency     = 'CNY',
       kind         = 'deepseek',
       created_at   = updated_at
 WHERE provider_id = 'deepseek' AND created_at = '';

CREATE TABLE IF NOT EXISTS provider_script_versions (
  id TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  script TEXT NOT NULL,
  actor_user_id TEXT,
  created_at TEXT NOT NULL,
  UNIQUE(provider_id,version)
);
CREATE INDEX IF NOT EXISTS ix_provider_script_versions_provider
  ON provider_script_versions(provider_id, version);
