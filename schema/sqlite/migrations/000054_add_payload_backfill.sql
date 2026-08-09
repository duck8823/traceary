-- Resumable in-place payload backfill bookkeeping.
-- Walks events and command_audits by a shared numeric rowid cursor up to a
-- per-table high-water fixed at run start. recipe_version refuses resume
-- across batch-semantic changes. Constant cost: CREATE only.

CREATE TABLE payload_backfill_runs (
  run_id TEXT PRIMARY KEY,
  recipe_version TEXT NOT NULL,
  -- Inclusive events.rowid ceiling fixed at run start. Independent of the
  -- command_audits sequence: the two tables do not share rowid allocation.
  high_water_rowid INTEGER NOT NULL CHECK (high_water_rowid >= 0),
  -- Inclusive command_audits.rowid ceiling fixed at run start. Stored
  -- separately so a later insert into the lagging table cannot land below a
  -- shared max and either strand the audit frontier or silently skip work.
  audit_high_water_rowid INTEGER NOT NULL DEFAULT 0 CHECK (audit_high_water_rowid >= 0),
  cursor_rowid INTEGER NOT NULL DEFAULT 0 CHECK (cursor_rowid >= 0),
  pass_count INTEGER NOT NULL DEFAULT 0 CHECK (pass_count >= 0),
  state TEXT NOT NULL CHECK (state IN ('running', 'paused', 'completed', 'failed')),
  -- Fences one worker off a run another worker took over. The active-run index
  -- admits one run row, not one worker: a second resume re-stamps this token,
  -- so the first worker's next checkpoint matches nothing and aborts instead of
  -- interleaving cursor writes and double-counting into the same run.
  worker_token TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT,
  scanned_rows INTEGER NOT NULL DEFAULT 0 CHECK (scanned_rows >= 0),
  encoded_rows INTEGER NOT NULL DEFAULT 0 CHECK (encoded_rows >= 0),
  identity_kept_rows INTEGER NOT NULL DEFAULT 0 CHECK (identity_kept_rows >= 0),
  conflicted_rows INTEGER NOT NULL DEFAULT 0 CHECK (conflicted_rows >= 0),
  partial_metadata_rows INTEGER NOT NULL DEFAULT 0 CHECK (partial_metadata_rows >= 0),
  rewritten_rows INTEGER NOT NULL DEFAULT 0 CHECK (rewritten_rows >= 0),
  plaintext_bytes INTEGER NOT NULL DEFAULT 0 CHECK (plaintext_bytes >= 0),
  stored_bytes INTEGER NOT NULL DEFAULT 0 CHECK (stored_bytes >= 0),
  failure_event_id TEXT,
  failure_reason TEXT
);

-- At most one resumable run may exist at a time.
CREATE UNIQUE INDEX idx_payload_backfill_active
  ON payload_backfill_runs ((1))
  WHERE state IN ('running', 'paused');
