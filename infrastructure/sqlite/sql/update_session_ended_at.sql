UPDATE sessions SET ended_at = ?, summary = CASE WHEN ? != '' THEN ? ELSE summary END WHERE session_id = ?
