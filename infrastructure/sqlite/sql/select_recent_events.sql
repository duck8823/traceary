SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE (? = '' OR e.kind = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = 0 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
   AND (? = '' OR e.created_at >= ?)
   AND (? = '' OR e.created_at < ?)
   AND (
         ? = ''
      OR e.source_hook = ?
      OR (
           ? = 'subagent_stop' AND e.kind = 'session_ended'
               AND e.body LIKE '[phase:subagent]%'
         )
      OR (
           ? = 'pre_compact' AND e.kind = 'compact_summary'
               AND e.body LIKE '[phase:pre-compact]%'
         )
       )
 ORDER BY e.created_at DESC, e.id DESC
 LIMIT ? OFFSET ?
