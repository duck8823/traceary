-- Sessions that have ended, or that the stale rule treats as ended, and that
-- may still have material past their refinement coverage. Same activity window
-- as session gc.
--
-- Ordered oldest-first by the earliest orphaned event's created_at (#1721),
-- not by session_id: CollectGarbage only needs the ranges it is about to
-- delete folded, and those are the oldest ones. A composite
-- (earliest_event_norm, session_id) keyset cursor keeps rows unique under
-- this order even when two sessions share the same earliest event time.
--
-- The coverage filter lives here, not in Go, because the caller applies a
-- LIMIT: against an unfiltered list a second pass would return the same
-- already-folded sessions and make no progress forever. Filtering here lets
-- SQLite stop as soon as enough rows match.
--
-- The filter is deliberately over-inclusive. Go re-checks each row with the
-- exact "latest strictly after covers_to" rule and drops the extras. Being
-- over-inclusive costs a wasted probe; being under-inclusive would skip a
-- session forever.
--
-- Canonical event order is (ts_norm(created_at), id): created_at is
-- variable-width RFC3339Nano and is not lexically ordered (#1185). Every
-- comparison and sort here uses the persisted created_at_norm column rather
-- than ts_norm(created_at) so idx_events_session_created_at_norm_id_desc
-- applies. sessions has no such column, so started_at still goes through
-- ts_norm.
--
-- Bind: cutoff (started_at), cutoff (last activity), cursor earliest_event_norm,
-- cursor session_id, limit.
WITH eligible_sessions AS (
  SELECT s.session_id AS session_id,
         (SELECT e2.created_at_norm
            FROM events AS e2
           WHERE e2.id = r.covers_to_event_id
             AND e2.session_id = s.session_id) AS covers_to_norm
    FROM sessions AS s
    LEFT JOIN session_refinements AS r ON r.session_id = s.session_id
   WHERE (
         s.ended_at IS NOT NULL
         OR (
              s.ended_at IS NULL
              AND ts_norm(s.started_at) < ts_norm(?)
              AND NOT EXISTS (
                    SELECT 1
                      FROM events AS e
                     WHERE e.session_id = s.session_id
                       AND e.created_at_norm >= ts_norm(?)
                  )
            )
       )
     AND EXISTS (SELECT 1 FROM events AS e3 WHERE e3.session_id = s.session_id)
     AND (
           r.session_id IS NULL
           OR r.covers_to_event_id IS NULL
           OR r.covers_to_event_id <> (
                SELECT e4.id
                  FROM events AS e4
                 WHERE e4.session_id = s.session_id
                 ORDER BY e4.created_at_norm DESC, e4.id DESC
                 LIMIT 1
              )
         )
),
candidates AS (
  SELECT es.session_id AS session_id,
         (SELECT e5.created_at
            FROM events AS e5
           WHERE e5.session_id = es.session_id
             AND (es.covers_to_norm IS NULL OR e5.created_at_norm > es.covers_to_norm)
           ORDER BY e5.created_at_norm ASC, e5.id ASC
           LIMIT 1) AS earliest_event_time,
         (SELECT e5.created_at_norm
            FROM events AS e5
           WHERE e5.session_id = es.session_id
             AND (es.covers_to_norm IS NULL OR e5.created_at_norm > es.covers_to_norm)
           ORDER BY e5.created_at_norm ASC, e5.id ASC
           LIMIT 1) AS earliest_event_norm
    FROM eligible_sessions AS es
)
SELECT session_id, earliest_event_time
  FROM candidates
 WHERE earliest_event_time IS NOT NULL
   AND (earliest_event_norm, session_id) > (?, ?)
 ORDER BY earliest_event_norm ASC, session_id ASC
 LIMIT ?
