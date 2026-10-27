-- Forward-only; never edit an applied file.
--
-- Schedules need to record failures so one user's broken schedule cannot block
-- every other user behind it in the due queue.

ALTER TABLE schedules ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE schedules ADD COLUMN last_attempt_at TEXT;
CREATE INDEX IF NOT EXISTS ix_runs_status_heartbeat ON runs(status, heartbeat_at);
