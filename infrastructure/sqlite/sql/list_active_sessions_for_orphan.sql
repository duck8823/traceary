-- Sessions that are neither ended nor stale (still active by the same 24h
-- activity rule as source 2) but that hold material past refinement coverage
-- older than the compact retention cutoff (#1724). An always-on session never
-- satisfies source 2's ended-or-stale predicate, so without this source its
-- pre-cutoff material would never surface as an orphan candidate even though
-- CollectGarbage's DELETE does not care about session lifecycle.
--
-- Ordered oldest-first by the earliest orphaned-and-pre-cutoff event's
-- created_at, matching list_ended_or_stale_sessions_for_orphan.sql's
-- convention (#1721) via the same composite keyset cursor.
--
-- earliest_event_time/earliest_event_norm are bound to events strictly older
-- than the retention cutoff: material at or after the cutoff is left alone.
-- Folding it here would claim coverage over events that have not aged into
-- deletion range yet, and a later activity burst must start a fresh orphan
-- range rather than extend this one.
--
-- Canonical event order is (ts_norm(created_at), id): created_at is
-- variable-width RFC3339Nano and is not lexically ordered (#1185). Every
-- comparison and sort here uses the persisted created_at_norm column rather
-- than ts_norm(created_at) so idx_events_session_created_at_norm_id_desc
-- applies. sessions has no such column, so started_at still goes through
-- ts_norm.
--
-- Bind: stale_cutoff (started_at), stale_cutoff (last activity),
-- retention_cutoff (material age, twice), cursor earliest_event_norm,
-- cursor session_id, limit.
WITH eligible_sessions AS (
  SELECT s.session_id AS session_id,
         (SELECT e2.created_at_norm
            FROM events AS e2
           WHERE e2.id = r.covers_to_event_id
             AND e2.session_id = s.session_id) AS covers_to_norm
    FROM sessions AS s
    LEFT JOIN session_refinements AS r ON r.session_id = s.session_id
   WHERE s.ended_at IS NULL
     AND NOT (
           ts_norm(s.started_at) < ts_norm(?)
           AND NOT EXISTS (
                 SELECT 1
                   FROM events AS e
                  WHERE e.session_id = s.session_id
                    AND e.created_at_norm >= ts_norm(?)
               )
         )
     AND EXISTS (SELECT 1 FROM events AS e3 WHERE e3.session_id = s.session_id)
),
candidates AS (
  SELECT es.session_id AS session_id,
         (SELECT e5.created_at
            FROM events AS e5
           WHERE e5.session_id = es.session_id
             AND (es.covers_to_norm IS NULL OR e5.created_at_norm > es.covers_to_norm)
             AND e5.created_at_norm < ts_norm(?)
             AND NULLIF(e5.created_at, '') IS NOT NULL
           ORDER BY e5.created_at_norm ASC, e5.id ASC
           LIMIT 1) AS earliest_event_time,
         (SELECT e5.created_at_norm
            FROM events AS e5
           WHERE e5.session_id = es.session_id
             AND (es.covers_to_norm IS NULL OR e5.created_at_norm > es.covers_to_norm)
             AND e5.created_at_norm < ts_norm(?)
             AND NULLIF(e5.created_at, '') IS NOT NULL
           ORDER BY e5.created_at_norm ASC, e5.id ASC
           LIMIT 1) AS earliest_event_norm
    FROM eligible_sessions AS es
)
SELECT session_id, earliest_event_time
  FROM candidates
 WHERE NULLIF(earliest_event_time, '') IS NOT NULL
   AND (earliest_event_norm, session_id) > (?, ?)
 ORDER BY earliest_event_norm ASC, session_id ASC
 LIMIT ?
