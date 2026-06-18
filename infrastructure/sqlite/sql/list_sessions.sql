WITH
  filtered_sessions AS (
    SELECT *
    FROM sessions s
    WHERE (? = '' OR s.session_id = ?)
      AND (? = '' OR s.workspace = ?)
      AND (? = '' OR s.client = ?)
      AND (? = '' OR s.agent = ? OR s.subagent_kind = ? OR EXISTS (SELECT 1 FROM events agent_events WHERE agent_events.session_id = s.session_id AND agent_events.agent = ?))
      AND (? = '' OR s.label = ?)
      AND (? = 0 OR s.ended_at IS NULL OR EXISTS (
            SELECT 1
              FROM events late_ev
             WHERE late_ev.session_id = s.session_id
               AND late_ev.created_at > s.ended_at
          ))
      AND (? = '' OR s.started_at >= ?)
      AND (? = '' OR s.started_at < ?)
    ORDER BY s.started_at DESC
    LIMIT ? OFFSET ?
  ),
  event_agg AS (
    SELECT
      e.session_id,
      COUNT(*) AS total_events,
      SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
      GROUP_CONCAT(DISTINCT e.agent) AS agents,
      MAX(e.created_at) AS latest_event_at
    FROM events e
    JOIN filtered_sessions fs ON fs.session_id = e.session_id
    GROUP BY e.session_id
  ),
  latest_events AS (
    SELECT session_id, kind AS latest_event_kind, body AS latest_event_body
    FROM (
      SELECT
        e.session_id,
        e.kind,
        e.body,
        ROW_NUMBER() OVER (
          PARTITION BY e.session_id
          ORDER BY e.created_at DESC, e.id DESC
        ) AS rn
      FROM events e
      JOIN filtered_sessions fs ON fs.session_id = e.session_id
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
  COALESCE(agg.latest_event_at, s.started_at) AS latest_event_at,
  COALESCE(agg.agents, '') AS agents,
  s.label,
  s.summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id,
  COALESCE(s.spawn_event_id, '') AS spawn_event_id,
  s.subagent_kind,
  s.spawn_order,
  COALESCE(latest.latest_event_kind, '') AS latest_event_kind,
  COALESCE(latest.latest_event_body, '') AS latest_event_body
FROM filtered_sessions s
LEFT JOIN event_agg agg ON agg.session_id = s.session_id
LEFT JOIN latest_events latest ON latest.session_id = s.session_id
ORDER BY s.started_at DESC
