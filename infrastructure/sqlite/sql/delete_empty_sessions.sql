DELETE FROM sessions
WHERE ended_at IS NOT NULL
  AND COALESCE(ended_at, started_at) < ?
  AND NOT EXISTS (
      SELECT 1
        FROM events
       WHERE events.session_id = sessions.session_id
  )
  AND NOT EXISTS (
      SELECT 1
        FROM sessions AS child_sessions
       WHERE child_sessions.parent_session_id = sessions.session_id
  )
