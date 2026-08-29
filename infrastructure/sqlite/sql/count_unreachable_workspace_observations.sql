SELECT COUNT(*)
  FROM session_workspace_observations
 WHERE NOT EXISTS (
       SELECT 1 FROM sessions s
        WHERE s.session_id = session_workspace_observations.session_id
   )
   AND NOT EXISTS (
       SELECT 1 FROM events e
        WHERE e.session_id = session_workspace_observations.session_id
   );
