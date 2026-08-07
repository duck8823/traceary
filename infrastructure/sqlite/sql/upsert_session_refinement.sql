-- Guarded upsert: coverage only advances under canonical event order
-- (ts_norm(created_at), id; see #1185). The events join below is part of this
-- single guarded write statement, not a general event-read API.
--
-- expectedGeneration = 0 means "expect no row". generation has CHECK
-- (generation > 0), so the UPDATE branch is unsatisfiable when a concurrent
-- insert already created a row — RowsAffected reports 0.
INSERT INTO session_refinements (
    session_id,
    generation,
    covers_from_event_id,
    covers_to_event_id,
    summary,
    keywords,
    produced_by,
    produced_at,
    degraded
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
    generation = excluded.generation,
    covers_from_event_id = excluded.covers_from_event_id,
    covers_to_event_id = excluded.covers_to_event_id,
    summary = excluded.summary,
    keywords = excluded.keywords,
    produced_by = excluded.produced_by,
    produced_at = excluded.produced_at,
    degraded = excluded.degraded
WHERE session_refinements.generation = ?
  AND (
        SELECT CASE
                 WHEN ts_norm(left_event.created_at) > ts_norm(right_event.created_at) THEN 1
                 WHEN ts_norm(left_event.created_at) < ts_norm(right_event.created_at) THEN 0
                 WHEN left_event.id > right_event.id THEN 1
                 ELSE 0
               END
          FROM events AS left_event
          JOIN events AS right_event ON right_event.id = session_refinements.covers_to_event_id
         WHERE left_event.id = excluded.covers_to_event_id
      ) = 1
