-- FindLatest first narrows the body-free projection to lifecycle events before
-- it orders candidates. Keep that selection index-backed even when the events
-- table contains mostly prompts, transcripts, and command audits.
CREATE INDEX idx_event_metadata_kind_created_at_norm_id_desc
    ON event_metadata_projection(kind, created_at_norm DESC, id DESC);
