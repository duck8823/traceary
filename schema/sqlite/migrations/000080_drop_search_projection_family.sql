-- 000080_drop_search_projection_family.sql
--
-- Drops the search projection family (13 tables plus the unread recent FTS
-- virtual table and its shadows). SQLite does not drop triggers when their
-- referenced tables disappear: a surviving events/command_audits trigger
-- would fail every later hook write with "no such table". DROP TRIGGER
-- therefore runs first. FK order: literal_search_fingerprints references
-- search_projection_source_sequence (000039), so that child drops before
-- the parent. Data-dependent: classified MigrationDataDependentOffline.
-- Applied on a run-owned candidate, never at live open.
--
-- Raises minimum_reader_version to 36 so older binaries fail loudly.
-- Does not drop event_metadata_projection (general read model) or
-- events_id_immutable (pillar identity guard; references no dropped table).

DROP TRIGGER IF EXISTS literal_search_event_insert;
DROP TRIGGER IF EXISTS literal_search_event_update;
DROP TRIGGER IF EXISTS literal_search_event_delete;
DROP TRIGGER IF EXISTS literal_search_audit_insert;
DROP TRIGGER IF EXISTS literal_search_audit_update;
DROP TRIGGER IF EXISTS literal_search_audit_delete;
DROP TRIGGER IF EXISTS search_projection_events_insert;
DROP TRIGGER IF EXISTS search_projection_events_update;
DROP TRIGGER IF EXISTS search_projection_events_delete;
DROP TRIGGER IF EXISTS search_projection_audits_insert;
DROP TRIGGER IF EXISTS search_projection_audits_update;
DROP TRIGGER IF EXISTS search_projection_audits_delete;
DROP TRIGGER IF EXISTS search_projection_complete_event_update;
DROP TRIGGER IF EXISTS search_projection_complete_event_delete;
DROP TRIGGER IF EXISTS search_projection_complete_audit_insert;
DROP TRIGGER IF EXISTS search_projection_complete_audit_update;
DROP TRIGGER IF EXISTS search_projection_complete_audit_delete;
DROP TABLE IF EXISTS literal_search_fingerprints;
DROP TABLE IF EXISTS search_projection_session_keywords;
DROP TABLE IF EXISTS search_projection_session_summaries;
DROP TABLE IF EXISTS search_projection_command_aggregates;
DROP TABLE IF EXISTS search_projection_recent_documents;
DROP TABLE IF EXISTS search_projection_recent_fts;
DROP TABLE IF EXISTS search_projection_exclusions;
DROP TABLE IF EXISTS search_projection_inventory_state;
DROP TABLE IF EXISTS search_projection_inventory_compat;
DROP TABLE IF EXISTS search_projection_source_revision;
DROP TABLE IF EXISTS search_projection_source_sequence;
DROP TABLE IF EXISTS search_projection_generation_lifecycle;
DROP TABLE IF EXISTS literal_search_projection_state;
DROP TABLE IF EXISTS search_projection_state;
UPDATE store_format_state SET minimum_reader_version = 36 WHERE singleton = 1;
