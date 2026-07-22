ALTER TABLE events ADD COLUMN body_availability TEXT NOT NULL DEFAULT 'available'
    CHECK (body_availability IN ('available', 'unavailable_retention'));
ALTER TABLE events ADD COLUMN body_pruned_at TEXT;
ALTER TABLE events ADD COLUMN body_pruned_plan_id TEXT;

DROP TRIGGER IF EXISTS events_body_metadata_after_body_update;
CREATE TRIGGER events_body_metadata_after_body_update
AFTER UPDATE OF body ON events
FOR EACH ROW
WHEN NEW.body_availability = 'available'
BEGIN
    UPDATE events
       SET body_stored_bytes = length(CAST(NEW.body AS BLOB))
     WHERE id = NEW.id;
END;

CREATE TABLE raw_body_retention_store_identity (
    id TEXT PRIMARY KEY NOT NULL,
    CHECK (length(id) = 32)
);

INSERT INTO raw_body_retention_store_identity (id)
VALUES (lower(hex(randomblob(16))));

CREATE TABLE raw_body_retention_executions (
    plan_id TEXT PRIMARY KEY NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'restored', 'conflicted')),
    candidate_count INTEGER NOT NULL CHECK (candidate_count >= 0),
    pruned_count INTEGER NOT NULL DEFAULT 0 CHECK (pruned_count >= 0),
    started_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE raw_body_retention_entries (
    plan_id TEXT NOT NULL REFERENCES raw_body_retention_executions(plan_id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    body_sha256 TEXT NOT NULL,
    stored_bytes INTEGER NOT NULL CHECK (stored_bytes >= 0),
    pruned_at TEXT NOT NULL,
    restored_at TEXT,
    PRIMARY KEY (plan_id, event_id)
);

CREATE INDEX idx_events_raw_body_retention_candidates
    ON events(body_availability, created_at, id);
