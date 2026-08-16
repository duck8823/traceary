-- Classify a bounded, operator-relevant exclusion set by the first
-- allowlist condition each row fails. Evaluation order matches
-- select_discardable_event_bodies.sql: kind, availability, session
-- identity, session ended, ts_valid, then coverage.
--
-- The outer scope is NOT "every age-eligible non-candidate". That would
-- serialize every historical prompt/note into the plan. Rows listed here
-- are either (1) the pre-#1762 set — available bodies in an active
-- session — or (2) available transcripts that still fail the allowlist
-- (ended/uncovered/missing/invalid-ts). already_discarded is therefore
-- not emitted as a per-row reason; those rows were never in the plan.
--
-- Parameters: ?, ?
--   ?1  cutoff timestamp for the outer ts_norm age scope
--   ?2  cutoff timestamp forwarded into the discardable-event-bodies subquery
SELECT e.id,
       CASE
           WHEN e.kind != 'transcript' THEN 'not_transcript'
           WHEN e.body_availability != 'available' THEN 'already_discarded'
           WHEN e.session_id IS NULL
             OR NOT EXISTS (
                 SELECT 1
                   FROM sessions AS s
                  WHERE s.session_id = e.session_id
             ) THEN 'session_missing'
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
   AND (
           (
               e.body_availability = 'available'
               AND EXISTS (
                   SELECT 1
                     FROM sessions AS s
                    WHERE s.session_id = e.session_id
                      AND s.ended_at IS NULL
               )
           )
           OR (
               e.kind = 'transcript'
               AND e.body_availability = 'available'
           )
       )
   AND NOT EXISTS (
           SELECT 1
             FROM (
-- discardable-event-bodies
             ) AS d
            WHERE d.id = e.id
       )
 ORDER BY e.id
