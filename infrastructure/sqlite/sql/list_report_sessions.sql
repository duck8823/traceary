WITH filtered_sessions AS (
    SELECT s.session_id, s.client, s.started_at
      FROM sessions s
     WHERE (? = '' OR s.workspace = ?)
       AND (? = '' OR s.client = ?)
       AND (? = '' OR ts_norm(s.started_at) >= ts_norm(?))
       AND (? = '' OR ts_norm(s.started_at) < ts_norm(?))
     ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
     LIMIT ? OFFSET ?
), event_agg AS (
    SELECT e.session_id,
           COUNT(*) AS total_events,
           SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count
      FROM events e
      JOIN filtered_sessions fs ON fs.session_id = e.session_id
     WHERE (? = '' OR e.workspace = ?)
       AND (? = '' OR e.client = ?)
       AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
       AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
     GROUP BY e.session_id
)
SELECT fs.client,
       fs.started_at,
       COALESCE(agg.total_events, 0),
       COALESCE(agg.command_count, 0)
  FROM filtered_sessions fs
  LEFT JOIN event_agg agg ON agg.session_id = fs.session_id
 ORDER BY ts_norm(fs.started_at) DESC, fs.session_id DESC
