-- Active session is stale when started before cutoff and no events exist at or
-- after cutoff. Bind: cutoff, cutoff, session_id.
SELECT CASE
         WHEN ts_norm(s.started_at) < ts_norm(?)
          AND NOT EXISTS (
                SELECT 1 FROM events AS e
                 WHERE e.session_id = s.session_id
                   AND ts_norm(e.created_at) >= ts_norm(?)
              )
         THEN 1 ELSE 0
       END
  FROM sessions AS s
 WHERE s.session_id = ?
