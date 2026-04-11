SELECT id, kind, client, agent, session_id, repo, body, created_at
  FROM events
 WHERE kind = ?
   AND session_id = ?
 ORDER BY created_at DESC, id DESC
 LIMIT 1
