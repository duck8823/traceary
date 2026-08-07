-- Returns 1 when left is strictly after right under canonical order
-- (ts_norm(created_at), id). Both ids must exist; callers validate first.
SELECT CASE
         WHEN ts_norm(left_event.created_at) > ts_norm(right_event.created_at) THEN 1
         WHEN ts_norm(left_event.created_at) < ts_norm(right_event.created_at) THEN 0
         WHEN left_event.id > right_event.id THEN 1
         ELSE 0
       END
  FROM events AS left_event
  JOIN events AS right_event ON right_event.id = ?
 WHERE left_event.id = ?
