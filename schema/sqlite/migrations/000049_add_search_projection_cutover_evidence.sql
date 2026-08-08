-- Durable cutover evidence for the bounded search projection family.
-- before/after measure only that family (search_projection_* + literal_search_*),
-- never the legacy migration-032 event_search_* family.
ALTER TABLE search_projection_state ADD COLUMN cutover_index_family TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN cutover_family_bytes_before INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_projection_state ADD COLUMN cutover_family_bytes_after INTEGER NOT NULL DEFAULT 0;
