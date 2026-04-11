SELECT id, kind, client, agent, session_id, repo, body, created_at
  FROM events
 WHERE (? = '' OR repo = ?)
   AND (? = '' OR session_id = ?)
 ORDER BY created_at DESC, id DESC
 LIMIT ?
