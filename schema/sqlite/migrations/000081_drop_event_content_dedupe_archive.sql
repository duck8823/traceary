-- 000081_drop_event_content_dedupe_archive.sql
--
-- Drops event_content_dedupe_archive after the candidate driver restores
-- remaining rows into events (or refuses). SQLite drops the table's indexes
-- with the table. No triggers reference the archive. Data-dependent:
-- classified MigrationDataDependentOffline. Applied on a run-owned
-- candidate, never at live open.
--
-- Restore-or-refuse of archive rows happens in Go before this SQL runs.
-- The SQL body stays a plain DROP (migration SQL carries no branching).
--
-- Raises minimum_reader_version to 37 so older binaries fail loudly.
-- DROP moves pages to the freelist; it does not shrink the file without
-- VACUUM / candidate rewrite.

DROP TABLE IF EXISTS event_content_dedupe_archive;
UPDATE store_format_state SET minimum_reader_version = 37 WHERE singleton = 1;
