CREATE TABLE IF NOT EXISTS sessions (
    session_id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    client TEXT NOT NULL DEFAULT '',
    agent TEXT NOT NULL DEFAULT '',
    repo TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT REFERENCES sessions(session_id)
);

CREATE INDEX IF NOT EXISTS idx_sessions_started_at
    ON sessions(started_at DESC);

CREATE INDEX IF NOT EXISTS idx_sessions_repo_started_at
    ON sessions(repo, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_sessions_parent
    ON sessions(parent_session_id);

-- Backfill from existing session_started/session_ended events
INSERT OR IGNORE INTO sessions (session_id, started_at, ended_at, client, agent, repo)
SELECT
    e.session_id,
    MIN(CASE WHEN e.kind = 'session_started' THEN e.created_at ELSE e.created_at END) AS started_at,
    MAX(CASE WHEN e.kind = 'session_ended' THEN e.created_at END) AS ended_at,
    COALESCE(
        MAX(CASE WHEN e.kind = 'session_started' THEN e.client END),
        MAX(e.client)
    ) AS client,
    COALESCE(
        MAX(CASE WHEN e.kind = 'session_started' THEN e.agent END),
        MAX(e.agent)
    ) AS agent,
    COALESCE(
        MAX(CASE WHEN e.kind = 'session_started' THEN e.repo END),
        MAX(e.repo)
    ) AS repo
FROM events e
GROUP BY e.session_id;
