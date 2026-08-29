-- Kind count strictly after a boundary under stored (created_at_norm, id) order.
-- Read the column, not ts_norm(created_at), so idx_events_session_created_at_norm_id_desc applies.
SELECT COUNT(*)
  FROM events AS e
  JOIN events AS boundary ON boundary.id = ?
 WHERE e.session_id = ?
   AND e.kind = ?
   AND (
         e.created_at_norm > boundary.created_at_norm
      OR (
             e.created_at_norm = boundary.created_at_norm
         AND e.id > boundary.id
         )
       )
