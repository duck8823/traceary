-- Canonical order is (created_at_norm, id). created_at_norm is the fixed-width
-- maintained projection of created_at (migration 000031 + its stamp triggers);
-- reading it directly — rather than wrapping created_at in ts_norm — keeps this
-- an index seek on idx_events_session_created_at_norm_id_desc.
SELECT id
  FROM events
 WHERE session_id = ?
 ORDER BY created_at_norm DESC, id DESC
 LIMIT 1;
