SELECT
  s.session_id,
  s.workspace,
  s.client,
  s.started_at,
  s.ended_at,
  COALESCE(agg.total_events, 0) AS total_events,
  COALESCE(agg.command_count, 0) AS command_count,
  COALESCE(agg.latest_event_at, s.started_at) AS latest_event_at,
  COALESCE(agg.agents, '') AS agents,
  s.label,
  s.summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id,
  COALESCE(s.spawn_event_id, '') AS spawn_event_id,
  s.subagent_kind,
  s.spawn_order
FROM sessions s
LEFT JOIN (
  SELECT
    e.session_id,
    COUNT(*) AS total_events,
    SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
    GROUP_CONCAT(DISTINCT e.agent) AS agents,
    MAX(e.created_at) AS latest_event_at
  FROM events e
  GROUP BY e.session_id
) agg ON agg.session_id = s.session_id
WHERE (? = '' OR s.session_id = ?)
  AND (? = '' OR s.workspace = ?)
  AND (? = '' OR s.client = ?)
  AND (? = '' OR s.agent = ?)
  AND (? = '' OR s.label = ?)
  AND (? = '' OR s.started_at >= ?)
  AND (? = '' OR s.started_at < ?)
ORDER BY s.started_at DESC
LIMIT ? OFFSET ?
