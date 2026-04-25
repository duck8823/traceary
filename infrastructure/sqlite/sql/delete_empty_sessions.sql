DELETE FROM sessions
WHERE ended_at IS NOT NULL
  AND COALESCE(ended_at, started_at) < ?
  AND NOT EXISTS (
      SELECT 1
        FROM events
       WHERE events.session_id = sessions.session_id
  )
