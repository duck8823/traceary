-- Rehearsal-only shadow storage. Canonical payloads remain identity plaintext.
CREATE TABLE payload_rehearsal_runs (
  run_id TEXT PRIMARY KEY,
  target_fingerprint TEXT NOT NULL,
  config_hash TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('running','paused','completed','scrubbed','rolled_back','failed')),
  event_high_water TEXT NOT NULL,
  audit_high_water TEXT NOT NULL,
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT,
  rollback_digest TEXT
);
CREATE UNIQUE INDEX idx_payload_rehearsal_active_target ON payload_rehearsal_runs(target_fingerprint)
WHERE state IN ('running','paused','completed','scrubbed');

CREATE TABLE payload_rehearsal_checkpoints (
  run_id TEXT NOT NULL REFERENCES payload_rehearsal_runs(run_id) ON DELETE CASCADE,
  table_kind TEXT NOT NULL CHECK(table_kind IN ('events','command_audits')),
  field_kind TEXT NOT NULL CHECK(field_kind IN ('body','command','input','output')),
  last_primary_key TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL CHECK(state IN ('pending','advancing','complete')),
  scanned_rows INTEGER NOT NULL DEFAULT 0 CHECK(scanned_rows >= 0),
  changed_rows INTEGER NOT NULL DEFAULT 0 CHECK(changed_rows >= 0),
  conflicted_rows INTEGER NOT NULL DEFAULT 0 CHECK(conflicted_rows >= 0),
  plaintext_bytes INTEGER NOT NULL DEFAULT 0 CHECK(plaintext_bytes >= 0),
  stored_bytes INTEGER NOT NULL DEFAULT 0 CHECK(stored_bytes >= 0),
  scrubbed_rows INTEGER NOT NULL DEFAULT 0 CHECK(scrubbed_rows >= 0),
  scrub_last_primary_key TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, table_kind, field_kind)
);

CREATE TABLE payload_rehearsal_rows (
  run_id TEXT NOT NULL REFERENCES payload_rehearsal_runs(run_id) ON DELETE CASCADE,
  table_kind TEXT NOT NULL,
  field_kind TEXT NOT NULL,
  source_primary_key TEXT NOT NULL,
  source_sha256 TEXT NOT NULL,
  payload BLOB NOT NULL,
  codec TEXT NOT NULL CHECK(codec = 'zstd'),
  format_version INTEGER NOT NULL CHECK(format_version = 1),
  plaintext_bytes INTEGER NOT NULL CHECK(plaintext_bytes >= 0),
  stored_bytes INTEGER NOT NULL CHECK(stored_bytes >= 0),
  payload_sha256 TEXT NOT NULL,
  PRIMARY KEY(run_id, table_kind, field_kind, source_primary_key)
);

CREATE TRIGGER payload_rehearsal_freeze_events_update BEFORE UPDATE ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused') AND OLD.id <= event_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_events_insert BEFORE INSERT ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused'))
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_events_delete BEFORE DELETE ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused') AND OLD.id <= event_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_update BEFORE UPDATE ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused') AND OLD.event_id <= audit_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_insert BEFORE INSERT ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused'))
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_delete BEFORE DELETE ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused') AND OLD.event_id <= audit_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
