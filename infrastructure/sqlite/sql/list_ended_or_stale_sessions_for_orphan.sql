-- Sessions that have ended, or that the stale rule treats as ended, and that
-- may still have material past their refinement coverage. Same activity window
-- as session gc.
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
-- variable-width RFC3339Nano and is not lexically ordered (#1185). The event
-- side uses the persisted created_at_norm column rather than ts_norm(created_at)
-- so idx_events_session_created_at_norm_id_desc applies: this query runs the
-- latest-event subquery for every session it scans, and a function call there
-- would turn each one into a scan-and-sort of that session's events. Migration
-- 031 backfills and trigger-maintains the column, and migrate_test pins
-- created_at_norm = ts_norm(created_at). sessions has no such column, so
-- started_at still goes through ts_norm.
--
-- Bind: after-cursor session_id, cutoff (started_at), cutoff (last activity), limit.
SELECT s.session_id
  FROM sessions AS s
  LEFT JOIN session_refinements AS r ON r.session_id = s.session_id
 WHERE s.session_id > ?
   AND (
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
              SELECT e2.id
                FROM events AS e2
               WHERE e2.session_id = s.session_id
               ORDER BY e2.created_at_norm DESC, e2.id DESC
               LIMIT 1
            )
       )
 ORDER BY s.session_id ASC
 LIMIT ?
