SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.repo, e.body, e.created_at
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE (? = '' OR e.kind = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.repo = ?)
   AND (? = 0 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
   AND (? = '' OR e.created_at >= ?)
   AND (? = '' OR e.created_at < ?)
 ORDER BY e.created_at DESC, e.id DESC
 LIMIT ? OFFSET ?
