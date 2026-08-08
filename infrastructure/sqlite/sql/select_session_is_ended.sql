SELECT CASE WHEN ended_at IS NOT NULL THEN 1 ELSE 0 END
  FROM sessions
 WHERE session_id = ?
