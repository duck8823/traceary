-- Classify events that are NOT retention candidates by the first allowlist
-- condition they fail, in the same evaluation order as
-- select_discardable_event_bodies.sql.  The outer scope limits to events
-- old enough to be age-eligible; within_retention fires only for events
-- whose timestamp fails ts_valid despite passing the lexical ts_norm check.
--
-- Parameters: ?, ?
--   ?1  cutoff timestamp for the outer ts_norm age scope
--   ?2  cutoff timestamp forwarded into the discardable-event-bodies subquery
SELECT e.id,
       CASE
           WHEN e.kind != 'transcript' THEN 'not_transcript'
           WHEN e.body_availability != 'available' THEN 'already_discarded'
           WHEN e.session_id IS NULL THEN 'session_missing'
           WHEN NOT EXISTS (
               SELECT 1
                 FROM sessions AS s
                WHERE s.session_id = e.session_id
                  AND s.ended_at IS NOT NULL
           ) THEN 'session_active'
           WHEN NOT ts_valid(e.created_at) THEN 'within_retention'
           ELSE 'uncovered'
       END AS reason
  FROM events AS e
 WHERE ts_norm(e.created_at) < ts_norm(?)
   AND NOT EXISTS (
           SELECT 1
             FROM (
-- discardable-event-bodies
             ) AS d
            WHERE d.id = e.id
       )
 ORDER BY e.id
