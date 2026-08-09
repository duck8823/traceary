-- Split active b-tree allocation of the bounded search index family into
-- three buckets: recent-tier, generation-scoped non-recent, and permanent
-- shared non-recent. Classification uses name AND tbl_name with GLOB for
-- recent so FTS shadow tables, idx_search_projection_recent_*, and
-- sqlite_autoindex_search_projection_recent_* land on the recent side
-- (LIKE '_' is a single-character wildcard and misclassifies).
--
-- Scoped is an explicit tbl_name set — not a glob — so a future table is
-- shared (conservative: fully reserved, never discounted by a generation
-- ratio) rather than silently apportioned. Shared includes permanently
-- resident objects such as search_projection_source_sequence that grow
-- forever and are never reclaimed by generation cleanup (#1679 MUST 2).
SELECT COALESCE(SUM(CASE WHEN recent THEN pgsize ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN scoped THEN pgsize ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN NOT recent AND NOT scoped THEN pgsize ELSE 0 END), 0)
  FROM (
        SELECT d.pgsize AS pgsize,
               (
                 s.name GLOB 'search_projection_recent*'
                 OR s.tbl_name GLOB 'search_projection_recent*'
               ) AS recent,
               (
                 s.tbl_name IN (
                   'search_projection_session_summaries',
                   'search_projection_session_keywords',
                   'search_projection_command_aggregates',
                   'literal_search_fingerprints'
                 )
               ) AS scoped
          FROM dbstat d
          JOIN sqlite_schema s ON s.name = d.name
         WHERE s.name GLOB 'search_projection_*'
            OR s.tbl_name GLOB 'search_projection_*'
            OR s.name GLOB 'literal_search_*'
            OR s.tbl_name GLOB 'literal_search_*'
       )
