-- Bounded variant of select_latest_event_id_for_session.sql: the last event
-- strictly before a given cutoff, not the session's true latest event. Used
-- by the third orphan-discovery source (#1724) so an active session's folded
-- terminus never claims coverage over events that have not aged past the
-- retention cutoff yet.
--
-- Canonical order is (ts_norm(created_at), id). created_at is variable-width
-- RFC3339Nano and is not lexically ordered (#1185).
-- Bind: session_id, cutoff.
SELECT id
  FROM events
 WHERE session_id = ?
   AND ts_norm(created_at) < ts_norm(?)
 ORDER BY ts_norm(created_at) DESC, id DESC
 LIMIT 1
