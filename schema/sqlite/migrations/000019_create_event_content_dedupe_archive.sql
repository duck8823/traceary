-- Additive support table for the reversible historical hook content dedupe
-- maintenance path (`traceary store dedupe content-events`, #1227).
--
-- This migration is intentionally additive only: it never moves, deletes, or
-- rewrites existing `events` rows. Quarantining historical duplicates is an
-- explicit, operator-initiated action (`--apply`); ordinary upgrade/migration
-- must not touch user data.
--
-- A row here is a duplicate event that `--apply` moved out of `events` so that
-- normal read surfaces (list, sessions --snapshot, doctor, context, MCP) stop
-- showing it. The table preserves every column needed to restore the original
-- `events` row verbatim (`--restore <run-id>`), plus the dedupe-run provenance.
CREATE TABLE IF NOT EXISTS event_content_dedupe_archive (
    -- Original events.id of the quarantined duplicate row. The original body is
    -- preserved verbatim (NOT the trimmed/normalized form used for grouping).
    id TEXT NOT NULL,
    kind TEXT NOT NULL,
    client TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    source_hook TEXT,
    -- The canonical row kept in `events` for this duplicate group (duplicate_of).
    kept_event_id TEXT NOT NULL,
    -- Provenance: the dedupe run that quarantined this row. `--restore` selects
    -- by this id; `--apply` is idempotent because the second run finds no
    -- duplicates left in `events` for an already-cleaned group.
    dedupe_run_id TEXT NOT NULL,
    archived_at TEXT NOT NULL,
    -- Human/forensic metadata describing why the row was quarantined and which
    -- identity group it belonged to (kind|client|agent|session|workspace|hook|body-hash).
    group_key TEXT NOT NULL,
    reason TEXT NOT NULL,
    -- An original event id can only be archived once per run; allowing the same
    -- id across distinct runs supports restore-then-reapply cycles.
    PRIMARY KEY (dedupe_run_id, id)
);

CREATE INDEX IF NOT EXISTS idx_event_content_dedupe_archive_run
    ON event_content_dedupe_archive(dedupe_run_id);
