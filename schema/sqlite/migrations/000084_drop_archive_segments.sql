-- 000084_drop_archive_segments.sql
--
-- Drops archive_segments after Go refuses when the table is non-empty.
-- Classified MigrationDataDependentOffline. Applied on a run-owned
-- candidate, never at live open (empty-store inline bootstrap may apply
-- this SQL only when the table has 0 rows).
--
-- Refuse-when-nonempty happens in Go before this SQL runs.
-- The SQL body stays a plain DROP (migration SQL carries no branching).
--
-- Raises minimum_reader_version to 40 so older binaries fail loudly.
-- DROP moves pages to the freelist; it does not shrink the file without
-- VACUUM / candidate rewrite.

DROP TABLE IF EXISTS archive_segments;
UPDATE store_format_state SET minimum_reader_version = 40 WHERE singleton = 1;
