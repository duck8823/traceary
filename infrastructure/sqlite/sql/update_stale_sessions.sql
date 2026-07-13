UPDATE sessions
SET ended_at = ?
WHERE ended_at IS NULL
  AND session_id <> ?
  AND ts_norm(started_at) < ts_norm(?)
  AND NOT EXISTS (
      SELECT 1
      FROM events AS e
      WHERE e.session_id = sessions.session_id
        AND ts_norm(e.created_at) >= ts_norm(?)
  )
