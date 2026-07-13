SELECT COUNT(*)
FROM sessions AS s
WHERE s.ended_at IS NULL
  AND ts_norm(s.started_at) < ts_norm(?)
  AND NOT EXISTS (
      SELECT 1
      FROM events AS e
      WHERE e.session_id = s.session_id
        AND ts_norm(e.created_at) >= ts_norm(?)
  )
