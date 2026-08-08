-- session_orphan_ranges records a range of a session's events that is past
-- the refinement's coverage and can no longer be folded by an agent.
--
-- Orphan discovery does not depend on this table: the source of truth is
-- session_refinements.covers_to on ended/stale sessions. A compact marker
-- only front-loads the same fact so a long session can discard bodies sooner.
--
-- from_event_id is an exclusive lower bound. Empty means the range starts at
-- the session's first event. to_event_id is inclusive. Primary key on
-- (session_id, to_event_id) makes re-recording the same boundary a no-op.
-- Event order is (ts_norm(created_at), id); never compare created_at as plain
-- TEXT (#1185).
CREATE TABLE session_orphan_ranges (
    session_id TEXT NOT NULL,
    from_event_id TEXT NOT NULL DEFAULT '',
    to_event_id TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (session_id, to_event_id)
);

CREATE INDEX idx_session_orphan_ranges_session_id
    ON session_orphan_ranges(session_id);
