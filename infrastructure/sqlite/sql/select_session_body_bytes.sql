-- Whole-session body-byte pressure (no refinement covers_to yet).
-- Measure UTF-8 storage size the same way body_stored_bytes is maintained.
SELECT COALESCE(SUM(length(CAST(body AS BLOB))), 0)
  FROM events
 WHERE session_id = ?
