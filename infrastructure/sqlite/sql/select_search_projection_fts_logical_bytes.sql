-- Integer columns count as 8 bytes each (SQL INTEGER / int64 width), not
-- length(CAST(<int> AS BLOB)) which is the decimal digit count. Blob/text
-- columns use length(CAST(x AS BLOB)). docsize.sz is a BLOB. data.id is
-- omitted (it was never in this SUM).
SELECT
  COALESCE((SELECT SUM(length(CAST(block AS BLOB))) FROM search_projection_recent_fts_data), 0)
+ COALESCE((SELECT SUM(length(CAST(term AS BLOB))) + COUNT(*) * 16 FROM search_projection_recent_fts_idx), 0)
+ COALESCE((SELECT SUM(length(CAST(sz AS BLOB))) + COUNT(*) * 8 FROM search_projection_recent_fts_docsize), 0)
+ COALESCE((SELECT SUM(length(CAST(k AS BLOB)) + CASE typeof(v) WHEN 'integer' THEN 8 ELSE length(CAST(v AS BLOB)) END) FROM search_projection_recent_fts_config), 0);
