SELECT e.id
  FROM events AS e
 WHERE e.kind = 'transcript'
   AND e.body_availability = 'available'
   AND e.session_id IS NOT NULL
   AND ts_norm(e.created_at) < ts_norm(?)
   AND EXISTS (
       SELECT 1
         FROM sessions AS s
        WHERE s.session_id = e.session_id
          AND s.ended_at IS NOT NULL
   )
   AND EXISTS (
       SELECT 1
         FROM session_refinements AS r
         JOIN events AS lo ON lo.id = r.covers_from_event_id
         JOIN events AS hi ON hi.id = r.covers_to_event_id
        WHERE r.session_id = e.session_id
          AND r.covers_from_event_id IS NOT NULL
          AND r.covers_to_event_id IS NOT NULL
          AND (ts_norm(lo.created_at), lo.id) <= (ts_norm(e.created_at), e.id)
          AND (ts_norm(e.created_at), e.id) <= (ts_norm(hi.created_at), hi.id)
   )
