SELECT e.id, e.client, e.agent, e.session_id, e.workspace,
       ca.command_wrapper, ca.command_name, ca.exit_code, ca.failed,
       ca.failure_reason, e.created_at
  FROM command_audits AS ca
  JOIN events AS e ON e.id = ca.event_id
 WHERE (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = '' OR e.created_at_norm >= ts_norm(?))
   AND (? = '' OR e.created_at_norm < ts_norm(?))
   AND (? = '' OR (e.created_at_norm, e.id) < (?, ?))
 ORDER BY e.created_at_norm DESC, e.id DESC
 LIMIT ?
