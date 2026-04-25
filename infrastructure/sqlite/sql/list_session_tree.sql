WITH RECURSIVE
  descendants(session_id, depth, path) AS (
    SELECT session_id, 0, ',' || session_id || ','
    FROM sessions
    WHERE session_id = ?
    UNION ALL
    SELECT child.session_id, descendants.depth + 1, descendants.path || child.session_id || ','
    FROM sessions child
    JOIN descendants ON child.parent_session_id = descendants.session_id
    WHERE descendants.depth < 100
      AND instr(descendants.path, ',' || child.session_id || ',') = 0
      AND (? = '' OR child.workspace = ?)
  ),
  candidate_ids(session_id) AS (
    SELECT s.session_id
    FROM sessions s
    WHERE (? = '' OR s.workspace = ?)
    ORDER BY s.started_at DESC
    LIMIT ?
  ),
  selected_ids(session_id) AS (
    SELECT descendants.session_id
    FROM descendants
    JOIN candidate_ids ON candidate_ids.session_id = descendants.session_id
    UNION
    SELECT session_id
    FROM descendants
    WHERE session_id = ?
  )
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
JOIN selected_ids ON selected_ids.session_id = s.session_id
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
ORDER BY
  s.parent_session_id NULLS FIRST,
  s.spawn_order NULLS FIRST,
  s.started_at ASC
