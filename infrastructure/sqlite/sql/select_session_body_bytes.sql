-- Whole-session body-byte pressure (no refinement covers_to yet).
SELECT COALESCE(SUM(length(CAST(body AS BLOB))), 0)
  FROM events
 WHERE session_id = ?
