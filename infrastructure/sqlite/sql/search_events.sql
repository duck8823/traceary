SELECT DISTINCT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.created_at
  FROM events e
  LEFT JOIN command_audits a ON a.event_id = e.id
 WHERE (? = '' OR
        e.body LIKE ? ESCAPE '\' OR
        COALESCE(a.command_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.input_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.output_text, '') LIKE ? ESCAPE '\')
   AND (? = '' OR e.workspace = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.kind = ?)
   AND (? = '' OR e.created_at >= ?)
   AND (? = '' OR e.created_at < ?)
   AND (? = 0 OR (a.exit_code IS NOT NULL AND a.exit_code != 0))
 ORDER BY e.created_at DESC, e.id DESC
 LIMIT ? OFFSET ?
