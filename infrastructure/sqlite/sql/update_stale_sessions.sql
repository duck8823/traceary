UPDATE sessions SET ended_at = ? WHERE ended_at IS NULL AND started_at < ?
