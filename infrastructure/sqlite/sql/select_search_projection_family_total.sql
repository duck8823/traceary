-- Total active b-tree allocation of the bounded search index family
-- (search_projection_* + literal_search_*). Same membership as the split
-- measurement; freelist pages are not counted.
SELECT COALESCE(SUM(pgsize), 0)
  FROM dbstat
 WHERE name IN (
         SELECT name
           FROM sqlite_schema
          WHERE name GLOB 'search_projection_*'
             OR tbl_name GLOB 'search_projection_*'
             OR name GLOB 'literal_search_*'
             OR tbl_name GLOB 'literal_search_*'
       )
