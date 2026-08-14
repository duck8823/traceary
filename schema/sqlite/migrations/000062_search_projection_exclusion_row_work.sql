-- Allow row_work exclusions (#1833). SQLite cannot ALTER a CHECK, so the
-- table is rebuilt. Existing rows only used stored_bytes / decoded_bytes /
-- write_bytes; the copy is one-to-one.
CREATE TABLE search_projection_exclusions_v2 (
    generation_id TEXT NOT NULL,
    source_sequence INTEGER NOT NULL,
    event_id TEXT NOT NULL,
    class TEXT NOT NULL CHECK(class IN('stored_bytes','decoded_bytes','write_bytes','row_work')),
    measured_bytes INTEGER NOT NULL,
    byte_limit INTEGER NOT NULL,
    PRIMARY KEY(generation_id,source_sequence)
);
INSERT INTO search_projection_exclusions_v2 (
    generation_id, source_sequence, event_id, class, measured_bytes, byte_limit
)
SELECT generation_id, source_sequence, event_id, class, measured_bytes, byte_limit
  FROM search_projection_exclusions;
DROP TABLE search_projection_exclusions;
ALTER TABLE search_projection_exclusions_v2 RENAME TO search_projection_exclusions;
CREATE INDEX idx_search_projection_exclusions_event
    ON search_projection_exclusions(generation_id,event_id);
