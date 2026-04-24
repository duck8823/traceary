SELECT id, kind, client, agent, session_id, workspace, body, source_hook, created_at
  FROM events
 WHERE (? = '' OR workspace = ?)
   AND (? = '' OR session_id = ?)
 ORDER BY created_at DESC, id DESC
 LIMIT ?
