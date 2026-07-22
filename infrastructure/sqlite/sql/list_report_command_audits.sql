SELECT e.id, e.client, e.agent, e.session_id, e.workspace,
       ca.command_wrapper, ca.command_name, ca.exit_code, ca.failed,
       ca.failure_reason, e.created_at
  FROM command_audits AS ca
  JOIN events AS e ON e.id = ca.event_id
 WHERE (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
   AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT ? OFFSET ?
