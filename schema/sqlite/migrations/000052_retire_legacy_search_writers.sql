-- Stop maintaining the migration-032 full-corpus search index family.
--
-- Constant-cost only: drop all eight writer triggers, the derived view, and
-- the authority/control table. The large tables (event_search_documents,
-- event_search_fts, event_search_backfill_state) stay until an operator runs
-- `traceary store search-retire`.
--
-- Do not DELETE/DROP/VACUUM the large tables here. Startup applies every
-- pending migration unconditionally; a multi-GiB DROP would block every
-- upgrade that still carries the family.

-- Source-side: nothing new enters the index.
DROP TRIGGER IF EXISTS events_search_after_insert;
DROP TRIGGER IF EXISTS events_search_after_body_update;
DROP TRIGGER IF EXISTS command_audits_search_after_insert;
DROP TRIGGER IF EXISTS command_audits_search_after_update;
DROP TRIGGER IF EXISTS command_audits_search_after_delete;

-- Documents-side. Dropping the source-side triggers is not enough to make the
-- family inert: event_search_documents.event_id carries
-- REFERENCES events(id) ON DELETE CASCADE and the standard DSN enables
-- foreign_keys, so deleting an event still cascades into the documents table
-- and fires these. The delete trigger would then append FTS5 delete markers --
-- growing the very index this retirement exists to remove -- on every gc and
-- retention pass.
DROP TRIGGER IF EXISTS event_search_documents_after_insert;
DROP TRIGGER IF EXISTS event_search_documents_after_delete;
DROP TRIGGER IF EXISTS event_search_documents_after_update;

DROP VIEW IF EXISTS event_search_projection;

DROP TABLE IF EXISTS search_maintenance_control;
