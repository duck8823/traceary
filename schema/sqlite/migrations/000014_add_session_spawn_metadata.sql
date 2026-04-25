ALTER TABLE sessions ADD COLUMN spawn_event_id TEXT;
ALTER TABLE sessions ADD COLUMN subagent_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN spawn_order INTEGER;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_spawn_order
    ON sessions(parent_session_id, spawn_order);
