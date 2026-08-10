SELECT e.body,
       e.created_at,
       e.body_availability,
       e.body_stored_bytes,
       CASE WHEN e.body_encoded_bytes IS NOT NULL
            THEN e.body_encoded_bytes
            ELSE length(CAST(e.body AS BLOB))
       END,
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
