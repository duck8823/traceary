SELECT id, kind, client, agent, session_id, workspace, body, body_availability, source_hook, created_at
  FROM events
 WHERE (? = '' OR workspace = ?)
   AND (? = '' OR session_id = ?)
 ORDER BY ts_norm(created_at) DESC, id DESC
 LIMIT ?
