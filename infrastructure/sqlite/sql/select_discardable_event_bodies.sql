-- The single allowlist for irreversible body discard, composed into the gc
-- count, the gc update, the retention plan and the retention apply-time
-- recheck. Every clause is a positive condition: a kind, a state or a column
-- this file has never heard of is excluded by default rather than by an
-- exclusion list that a later migration would have to remember to extend.
--
-- ts_valid pairs with every ts_norm over a stored column here. ts_norm
-- degrades an unparseable timestamp to lexical comparison so ordinary reads
-- survive historical rows, but an event whose age cannot be determined must
-- not be discarded on the strength of a comparison that only happened to
-- succeed. The cutoff parameter needs no such guard: Go formats it.
SELECT e.id
  FROM events AS e
 WHERE e.kind = 'transcript'
   AND e.body_availability = 'available'
   AND e.session_id IS NOT NULL
   AND ts_valid(e.created_at)
   AND ts_norm(e.created_at) < ts_norm(?)
   AND EXISTS (
       SELECT 1
         FROM sessions AS s
        WHERE s.session_id = e.session_id
          AND s.ended_at IS NOT NULL
   )
   AND EXISTS (
       -- The boundary events must belong to the same session as the
       -- refinement that names them. Nothing in the schema enforces that, and
       -- without it a refinement holding another session's ids would place
       -- this event inside a range that never covered it.
       SELECT 1
         FROM session_refinements AS r
         JOIN events AS lo
           ON lo.id = r.covers_from_event_id
          AND lo.session_id = r.session_id
         JOIN events AS hi
           ON hi.id = r.covers_to_event_id
          AND hi.session_id = r.session_id
        WHERE r.session_id = e.session_id
          AND r.covers_from_event_id IS NOT NULL
          AND r.covers_to_event_id IS NOT NULL
          AND ts_valid(lo.created_at)
          AND ts_valid(hi.created_at)
          AND (ts_norm(lo.created_at), lo.id) <= (ts_norm(e.created_at), e.id)
          AND (ts_norm(e.created_at), e.id) <= (ts_norm(hi.created_at), hi.id)
   )
