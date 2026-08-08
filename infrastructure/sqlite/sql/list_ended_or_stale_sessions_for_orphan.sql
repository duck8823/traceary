-- Sessions that have ended, or that the stale rule treats as ended:
-- active (ended_at IS NULL) with no events at or after the activity cutoff
-- and started before the cutoff. Same activity window as session gc.
-- Bind: cutoff twice (started_at compare, last-activity compare).
SELECT s.session_id
  FROM sessions AS s
 WHERE s.ended_at IS NOT NULL
    OR (
         s.ended_at IS NULL
         AND ts_norm(s.started_at) < ts_norm(?)
         AND NOT EXISTS (
               SELECT 1
                 FROM events AS e
                WHERE e.session_id = s.session_id
                  AND ts_norm(e.created_at) >= ts_norm(?)
             )
       )
