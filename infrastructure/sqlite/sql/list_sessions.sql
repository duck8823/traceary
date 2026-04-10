SELECT
  s.session_id,
  s.repo,
  s.started_at,
  s.ended_at,
  COALESCE(agg.total_events, 0) AS total_events,
  COALESCE(agg.command_count, 0) AS command_count,
  COALESCE(agg.agents, '') AS agents,
  s.label,
  s.summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id
FROM sessions s
LEFT JOIN (
  SELECT
    e.session_id,
    COUNT(*) AS total_events,
    SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
    GROUP_CONCAT(DISTINCT e.agent) AS agents
  FROM events e
  GROUP BY e.session_id
) agg ON agg.session_id = s.session_id
WHERE (? = '' OR s.repo = ?)
  AND (? = '' OR s.agent = ?)
  AND (? = '' OR s.label = ?)
  AND (? = '' OR s.started_at >= ?)
  AND (? = '' OR s.started_at < ?)
ORDER BY s.started_at DESC
LIMIT ? OFFSET ?
