-- Metadata list/context queries must preserve RFC3339Nano instant ordering.
-- created_at itself is variable-width and cannot provide that order around an
-- exact-second/fractional-second boundary, so index the deterministic
-- normalization expression used by the query contract.
--
-- These indexes are additive: they neither rewrite event rows nor remove
-- existing indexes. Rolling back application code therefore leaves a fully
-- readable store; a later maintenance release may drop only these named
-- indexes after all readers no longer rely on them.
CREATE INDEX IF NOT EXISTS idx_events_ts_norm_created_at_id_desc
    ON events(ts_norm(created_at) DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_events_workspace_ts_norm_created_at_id_desc
    ON events(workspace, ts_norm(created_at) DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_events_session_ts_norm_created_at_id_desc
    ON events(session_id, ts_norm(created_at) DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_events_workspace_session_ts_norm_created_at_id_desc
    ON events(workspace, session_id, ts_norm(created_at) DESC, id DESC);
