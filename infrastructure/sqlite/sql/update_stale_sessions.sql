UPDATE sessions
SET ended_at = ?,
    terminal_reason = 'legacy_unknown'
WHERE ended_at IS NULL
  /* protected sessions */
  AND ts_norm(started_at) < ts_norm(?)
  AND NOT EXISTS (
      SELECT 1
      FROM events AS e
      WHERE e.session_id = sessions.session_id
        AND ts_norm(e.created_at) >= ts_norm(?)
  )
