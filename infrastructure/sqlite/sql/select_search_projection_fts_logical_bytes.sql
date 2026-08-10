SELECT
  COALESCE((SELECT SUM(length(CAST(block AS BLOB))) FROM search_projection_recent_fts_data), 0)
+ COALESCE((SELECT SUM(length(CAST(segid AS BLOB)) + length(CAST(term AS BLOB)) + length(CAST(pgno AS BLOB))) FROM search_projection_recent_fts_idx), 0)
 + COALESCE((SELECT SUM(length(CAST(id AS BLOB)) + length(CAST(sz AS BLOB))) FROM search_projection_recent_fts_docsize), 0)
 + COALESCE((SELECT SUM(length(CAST(k AS BLOB)) + length(CAST(v AS BLOB))) FROM search_projection_recent_fts_config), 0);
