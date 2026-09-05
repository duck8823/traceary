SELECT e.body,
       e.created_at,
       e.body_availability,
       e.body_stored_bytes,
       length(CAST(e.body AS BLOB)),
       e.body_pruned_plan_id,
       EXISTS (
           SELECT 1
             FROM (
-- discardable-event-bodies
             ) AS d
            WHERE d.id = ?
       )
  FROM events AS e
 WHERE e.id = ?
