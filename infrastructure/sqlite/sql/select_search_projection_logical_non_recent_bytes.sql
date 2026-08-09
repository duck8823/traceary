-- Logical non-recent-tier bytes for one generation, or the whole family when
-- the bound generation_id is the empty string. The per-row expressions match
-- selectProjectionCleanup's cleanup accounting for summary, aggregate,
-- keyword and fingerprint rows — keep them in lockstep; do not transcribe a
-- second copy in Go. Used only to apportion measured non-recent physical
-- pages to the generation that will survive a rebuild (#1679 MUST 4b).
WITH target(g) AS (SELECT ?)
SELECT COALESCE((
  SELECT SUM(n) FROM (
    SELECT length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(summary_text AS BLOB))+24 AS n
      FROM search_projection_session_summaries
     WHERE (SELECT g FROM target) = '' OR generation_id = (SELECT g FROM target)
    UNION ALL
    SELECT length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+16
      FROM search_projection_command_aggregates
     WHERE (SELECT g FROM target) = '' OR generation_id = (SELECT g FROM target)
    UNION ALL
    SELECT length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(keyword AS BLOB))+16
      FROM search_projection_session_keywords
     WHERE (SELECT g FROM target) = '' OR generation_id = (SELECT g FROM target)
    UNION ALL
    SELECT length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16
      FROM literal_search_fingerprints
     WHERE (SELECT g FROM target) = '' OR generation_id = (SELECT g FROM target)
  )
), 0)
