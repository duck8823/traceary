-- An observation is unreachable once neither its session row nor any event of
-- that session survives. Compact's copy filter, content dedupe, and empty-session
-- GC all leave such rows behind; migration 000072 documents the orphan case.
-- The events clause is not optional: events have no FK to sessions (000001), so
-- a recorded orphan range is session-row-less but still live data.
DELETE FROM session_workspace_observations
 WHERE NOT EXISTS (
       SELECT 1 FROM sessions s
        WHERE s.session_id = session_workspace_observations.session_id
   )
   AND NOT EXISTS (
       SELECT 1 FROM events e
        WHERE e.session_id = session_workspace_observations.session_id
   );
