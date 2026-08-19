WITH filtered_sessions AS (
    SELECT s.session_id, s.client, s.started_at, s.started_at_norm
      FROM sessions s
     WHERE (? = '' OR s.workspace = ?)
       AND (? = '' OR s.client = ?)
       AND (? = '' OR s.started_at_norm >= ts_norm(?))
       AND (? = '' OR s.started_at_norm < ts_norm(?))
       AND (? = '' OR (s.started_at_norm, s.session_id) < (?, ?))
     ORDER BY s.started_at_norm DESC, s.session_id DESC
     LIMIT ?
), event_agg AS (
    SELECT e.session_id,
           COUNT(*) AS total_events,
           SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count
      FROM events e
      JOIN filtered_sessions fs ON fs.session_id = e.session_id
     WHERE (? = '' OR e.workspace = ?)
       AND (? = '' OR e.client = ?)
       AND (? = '' OR e.created_at_norm >= ts_norm(?))
       AND (? = '' OR e.created_at_norm < ts_norm(?))
     GROUP BY e.session_id
)
SELECT fs.session_id,
       fs.client,
       fs.started_at,
       COALESCE(agg.total_events, 0),
       COALESCE(agg.command_count, 0)
  FROM filtered_sessions fs
  LEFT JOIN event_agg agg ON agg.session_id = fs.session_id
 ORDER BY fs.started_at_norm DESC, fs.session_id DESC
