-- Split active b-tree allocation of the bounded search index family into
-- recent-tier objects vs everything else. Classification uses name AND
-- tbl_name with GLOB so FTS shadow tables, idx_search_projection_recent_*,
-- and sqlite_autoindex_search_projection_recent_* land on the recent side
-- (LIKE '_' is a single-character wildcard and misclassifies).
-- A name-only '*search_projection_recent*' index clause is unreachable: every
-- index on a recent table already matches on tbl_name, and a leading '*' would
-- let an unrelated object whose name merely contains the string join recent.
SELECT COALESCE(SUM(CASE WHEN recent THEN pgsize ELSE 0 END), 0),
       COALESCE(SUM(CASE WHEN recent THEN 0 ELSE pgsize END), 0)
  FROM (
        SELECT d.pgsize AS pgsize,
               (
                 s.name GLOB 'search_projection_recent*'
                 OR s.tbl_name GLOB 'search_projection_recent*'
               ) AS recent
          FROM dbstat d
          JOIN sqlite_schema s ON s.name = d.name
         WHERE s.name GLOB 'search_projection_*'
            OR s.tbl_name GLOB 'search_projection_*'
            OR s.name GLOB 'literal_search_*'
            OR s.tbl_name GLOB 'literal_search_*'
       )
