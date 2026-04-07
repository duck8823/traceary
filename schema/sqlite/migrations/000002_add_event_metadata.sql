ALTER TABLE events ADD COLUMN client TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN repo TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_events_created_at
    ON events(created_at DESC, id DESC);
