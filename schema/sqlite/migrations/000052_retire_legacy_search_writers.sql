-- Stop maintaining the migration-032 full-corpus search index family.
--
-- Constant-cost only: drop the five source-side writer triggers, the
-- derived view, and the authority/control table. The large tables
-- (event_search_documents, event_search_fts, event_search_backfill_state)
-- stay until an operator runs `traceary store search-retire`.
--
-- Do not DELETE/DROP/VACUUM the large tables here. Startup applies every
-- pending migration unconditionally; a multi-GiB DROP would block every
-- upgrade that still carries the family.

DROP TRIGGER IF EXISTS events_search_after_insert;
DROP TRIGGER IF EXISTS events_search_after_body_update;
DROP TRIGGER IF EXISTS command_audits_search_after_insert;
DROP TRIGGER IF EXISTS command_audits_search_after_update;
DROP TRIGGER IF EXISTS command_audits_search_after_delete;

DROP VIEW IF EXISTS event_search_projection;

DROP TABLE IF EXISTS search_maintenance_control;
