UPDATE sessions
SET ended_at = ?,
    terminal_reason = ?,
    summary = CASE WHEN ? = '' THEN summary ELSE ? END
WHERE session_id = ?
  AND ended_at IS NULL
