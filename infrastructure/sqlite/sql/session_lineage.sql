WITH RECURSIVE
  ancestors(session_id, parent_session_id, depth) AS (
    SELECT session_id, parent_session_id, 0
    FROM sessions
    WHERE session_id = ?
    UNION ALL
    SELECT parent.session_id, parent.parent_session_id, ancestors.depth + 1
    FROM sessions parent
    JOIN ancestors ON ancestors.parent_session_id = parent.session_id
  ),
  lineage_root(session_id) AS (
    SELECT session_id
    FROM ancestors
    ORDER BY depth DESC
    LIMIT 1
  ),
  lineage(session_id) AS (
    SELECT session_id
    FROM lineage_root
    UNION ALL
    SELECT child.session_id
    FROM sessions child
    JOIN lineage ON child.parent_session_id = lineage.session_id
  )
SELECT
  s.session_id,
  s.workspace,
  s.started_at,
  s.ended_at,
  COALESCE(agg.total_events, 0) AS total_events,
  COALESCE(agg.command_count, 0) AS command_count,
  COALESCE(agg.agents, '') AS agents,
  s.label,
  s.summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id,
  COALESCE(s.spawn_event_id, '') AS spawn_event_id,
  s.subagent_kind,
  s.spawn_order
FROM sessions s
JOIN lineage ON lineage.session_id = s.session_id
LEFT JOIN (
  SELECT
    e.session_id,
    COUNT(*) AS total_events,
    SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
    GROUP_CONCAT(DISTINCT e.agent) AS agents
  FROM events e
  GROUP BY e.session_id
) agg ON agg.session_id = s.session_id
ORDER BY
  s.parent_session_id NULLS FIRST,
  s.spawn_order NULLS FIRST,
  s.started_at ASC
