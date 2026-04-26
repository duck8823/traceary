UPDATE sessions SET summary = ? WHERE session_id = ? AND (summary IS NULL OR summary = '')
