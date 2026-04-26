CREATE INDEX IF NOT EXISTS idx_events_session_created_at_id_desc
ON events(session_id, created_at DESC, id DESC);
