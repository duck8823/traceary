SELECT CASE
         WHEN COALESCE(parent_session_id, '') != '' THEN 0
         WHEN subagent_kind != '' THEN 0
         WHEN instr(agent, '/') != 0 THEN 0
         ELSE 1
       END
  FROM sessions
 WHERE session_id = ?
