-- session_refinements holds the L2 session summary layer (one row per session).
--
-- One row per session, not one row per generation: a newer refinement strictly
-- subsumes its predecessor (coverage only advances; re-summarisation is
-- cumulative), so keeping older generations would store duplicated data in the
-- store this sprint exists to shrink. generation counts how many merges
-- produced the row that is currently stored.
--
-- Coverage is stored as event ids only, not denormalised timestamps. events
-- rows are permanent in this design (only bodies are ever discarded), so an id
-- can always be resolved to its position. Event order is (created_at, id);
-- created_at is variable-width RFC3339Nano and is *not* lexically ordered —
-- comparisons must use the ts_norm SQL function (see #1185).
CREATE TABLE session_refinements (
    session_id TEXT PRIMARY KEY,
    generation INTEGER NOT NULL CHECK (generation > 0),
    covers_from_event_id TEXT NOT NULL,
    covers_to_event_id TEXT NOT NULL,
    summary TEXT NOT NULL CHECK (length(summary) > 0),
    keywords TEXT NOT NULL DEFAULT '',
    produced_by TEXT NOT NULL CHECK (length(produced_by) > 0),
    produced_at TEXT NOT NULL,
    degraded INTEGER NOT NULL CHECK (degraded IN (0, 1))
);
