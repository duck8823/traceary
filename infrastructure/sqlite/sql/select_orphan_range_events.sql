-- Events in an orphan range under canonical order (ts_norm(created_at), id).
-- Bind order: session_id, from_event_id (empty-check + exclusive lower), to_event_id.
SELECT e.id, e.kind, e.created_at
  FROM events AS e
 WHERE e.session_id = ?
   AND (
         ? = ''
         OR (
              SELECT CASE
                       WHEN ts_norm(e.created_at) > ts_norm(lower_bound.created_at) THEN 1
                       WHEN ts_norm(e.created_at) < ts_norm(lower_bound.created_at) THEN 0
                       WHEN e.id > lower_bound.id THEN 1
                       ELSE 0
                     END
                FROM events AS lower_bound
               WHERE lower_bound.id = ?
            ) = 1
       )
   AND (
         SELECT CASE
                  WHEN ts_norm(e.created_at) < ts_norm(upper_bound.created_at) THEN 1
                  WHEN ts_norm(e.created_at) > ts_norm(upper_bound.created_at) THEN 0
                  WHEN e.id <= upper_bound.id THEN 1
                  ELSE 0
                END
           FROM events AS upper_bound
          WHERE upper_bound.id = ?
       ) = 1
 ORDER BY ts_norm(e.created_at) ASC, e.id ASC
