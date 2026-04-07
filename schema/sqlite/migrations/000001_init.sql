CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_session_created_at
    ON events(session_id, created_at);
