SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.created_at,
       ca.command_text, ca.input_text, ca.output_text, ca.input_truncated, ca.output_truncated, ca.exit_code
  FROM events AS e
  LEFT JOIN command_audits AS ca
    ON ca.event_id = e.id
 WHERE e.id = ?
