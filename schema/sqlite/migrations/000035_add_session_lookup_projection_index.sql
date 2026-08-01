-- FindLatest first narrows the body-free projection to lifecycle events before
-- it orders candidates. Keep that selection index-backed even when the events
-- table contains mostly prompts, transcripts, and command audits.
CREATE INDEX idx_event_metadata_kind_created_at_norm_id_desc
    ON event_metadata_projection(kind, created_at_norm DESC, id DESC);

-- Global lifecycle ordering for latest-session lookup. The context columns
-- keep boundary-to-start matching off body-bearing event pages.
CREATE INDEX idx_event_metadata_boundary_time_context
    ON event_metadata_projection(created_at_norm DESC, id DESC, session_id, client, agent, workspace)
    WHERE kind IN ('session_started', 'session_ended');
