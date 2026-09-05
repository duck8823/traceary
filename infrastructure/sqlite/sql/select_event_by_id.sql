SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.body, e.source_hook, e.created_at
  FROM events e
 WHERE e.id = ?
