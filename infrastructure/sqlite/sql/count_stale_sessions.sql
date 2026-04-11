SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL AND started_at < ?
