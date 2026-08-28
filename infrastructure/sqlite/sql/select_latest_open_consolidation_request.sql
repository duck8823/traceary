SELECT session_id, client, requested_at, at_event_id, signal, pressure_value, threshold_value, re_request, delivery
  FROM consolidation_requests
 WHERE session_id = ?
   AND refine_outcome IS NULL
 ORDER BY id DESC
 LIMIT 1;
