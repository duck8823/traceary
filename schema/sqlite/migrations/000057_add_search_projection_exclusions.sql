CREATE TABLE search_projection_exclusions(
 generation_id TEXT NOT NULL,
 source_sequence INTEGER NOT NULL,
 event_id TEXT NOT NULL,
 class TEXT NOT NULL CHECK(class IN('stored_bytes','decoded_bytes','write_bytes')),
 measured_bytes INTEGER NOT NULL,
 byte_limit INTEGER NOT NULL,
 PRIMARY KEY(generation_id,source_sequence)
);
CREATE INDEX idx_search_projection_exclusions_event ON search_projection_exclusions(generation_id,event_id);
