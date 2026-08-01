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
  rollback_digest TEXT,
  target_device TEXT NOT NULL,
  target_inode TEXT NOT NULL,
  lease_token TEXT,
  lease_expires_at TEXT
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
  table_kind TEXT NOT NULL CHECK(table_kind IN ('events','command_audits')),
  field_kind TEXT NOT NULL CHECK(field_kind IN ('body','command','input','output')),
  source_primary_key TEXT NOT NULL,
  source_sha256 TEXT NOT NULL,
  payload BLOB NOT NULL,
  codec TEXT NOT NULL CHECK(codec = 'zstd'),
  format_version INTEGER NOT NULL CHECK(format_version = 1),
  plaintext_bytes INTEGER NOT NULL CHECK(plaintext_bytes >= 0),
  stored_bytes INTEGER NOT NULL CHECK(stored_bytes >= 0),
  payload_sha256 TEXT NOT NULL,
  CHECK((table_kind='events' AND field_kind='body') OR (table_kind='command_audits' AND field_kind IN ('command','input','output'))),
  PRIMARY KEY(run_id, table_kind, field_kind, source_primary_key)
);

-- Once encoding has completed, scrub readers may span multiple bounded read
-- transactions. Freeze the shadow evidence so a writer cannot change an
-- already verified page before its checkpoint/final transition commits.
CREATE TRIGGER payload_rehearsal_freeze_rows_insert BEFORE INSERT ON payload_rehearsal_rows
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE run_id=NEW.run_id AND state IN ('completed','scrubbed'))
BEGIN SELECT RAISE(ABORT, 'completed payload rehearsal rows are immutable'); END;
CREATE TRIGGER payload_rehearsal_freeze_rows_update BEFORE UPDATE ON payload_rehearsal_rows
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE run_id=OLD.run_id AND state IN ('completed','scrubbed'))
BEGIN SELECT RAISE(ABORT, 'completed payload rehearsal rows are immutable'); END;
CREATE TRIGGER payload_rehearsal_freeze_rows_delete BEFORE DELETE ON payload_rehearsal_rows
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE run_id=OLD.run_id AND state IN ('completed','scrubbed'))
BEGIN SELECT RAISE(ABORT, 'completed payload rehearsal rows are immutable'); END;

CREATE TRIGGER payload_rehearsal_freeze_events_update BEFORE UPDATE ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed') AND OLD.id <= event_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_events_insert BEFORE INSERT ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed'))
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_events_delete BEFORE DELETE ON events
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed') AND OLD.id <= event_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_update BEFORE UPDATE ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed') AND OLD.event_id <= audit_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_insert BEFORE INSERT ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed'))
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
CREATE TRIGGER payload_rehearsal_freeze_audits_delete BEFORE DELETE ON command_audits
WHEN EXISTS(SELECT 1 FROM payload_rehearsal_runs WHERE state IN ('running','paused','completed') AND OLD.event_id <= audit_high_water)
BEGIN SELECT RAISE(ABORT, 'payload rehearsal source is frozen'); END;
