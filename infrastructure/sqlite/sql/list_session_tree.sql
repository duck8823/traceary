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
  selected_ids(session_id) AS (
    SELECT session_id
    FROM descendants
  ),
  event_agg AS (
    SELECT
      e.session_id,
      COUNT(*) AS total_events,
      SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
      GROUP_CONCAT(DISTINCT e.agent) AS agents
    FROM events e
    JOIN selected_ids ON selected_ids.session_id = e.session_id
    GROUP BY e.session_id
  ),
  latest_events AS (
    SELECT session_id, id AS latest_event_id, created_at AS latest_event_at, kind AS latest_event_kind, body AS latest_event_body
    FROM (
      SELECT
        e.session_id,
        e.id,
        e.created_at,
        e.kind,
        e.body,
        ROW_NUMBER() OVER (
          PARTITION BY e.session_id
          ORDER BY ts_norm(e.created_at) DESC, e.id DESC
        ) AS rn
      FROM events e
      JOIN selected_ids ON selected_ids.session_id = e.session_id
    )
    WHERE rn = 1
  )
SELECT
  s.session_id,
  s.workspace,
  s.client,
  s.started_at,
  s.ended_at,
  COALESCE(agg.total_events, 0) AS total_events,
  COALESCE(agg.command_count, 0) AS command_count,
  COALESCE(latest.latest_event_at, s.started_at) AS latest_event_at,
  COALESCE(agg.agents, '') AS agents,
  s.label,
  s.summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id,
  COALESCE(s.spawn_event_id, '') AS spawn_event_id,
  s.subagent_kind,
  s.spawn_order,
  COALESCE(latest.latest_event_kind, '') AS latest_event_kind,
  COALESCE(latest.latest_event_id, '') AS latest_event_id,
  COALESCE(latest.latest_event_body, '') AS latest_event_body
FROM sessions s
JOIN selected_ids ON selected_ids.session_id = s.session_id
LEFT JOIN event_agg agg ON agg.session_id = s.session_id
LEFT JOIN latest_events latest ON latest.session_id = s.session_id
ORDER BY
  s.parent_session_id NULLS FIRST,
  s.spawn_order NULLS FIRST,
  s.started_at ASC
