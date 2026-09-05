SELECT e.id, e.created_at, e.body_stored_bytes,
       length(CAST(e.body AS BLOB)),
       e.body
  FROM (
-- discardable-event-bodies
  ) AS d
  JOIN events AS e ON e.id = d.id
 ORDER BY ts_norm(e.created_at), e.id
