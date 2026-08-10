SELECT e.id, e.created_at, e.body_stored_bytes,
       CASE WHEN e.body_encoded_bytes IS NOT NULL
            THEN e.body_encoded_bytes
            ELSE length(CAST(e.body AS BLOB))
       END,
       e.body
  FROM (
-- discardable-event-bodies
  ) AS d
  JOIN events AS e ON e.id = d.id
 ORDER BY ts_norm(e.created_at), e.id
