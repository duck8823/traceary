WITH
  filtered_sessions AS (
    SELECT *
    FROM sessions s
    WHERE (? = '' OR s.session_id = ?)
      AND (? = '' OR s.workspace = ?)
      AND (? = '' OR s.client = ?)
      AND (? = '' OR s.agent = ? OR s.subagent_kind = ? OR EXISTS (SELECT 1 FROM event_metadata_projection agent_events WHERE agent_events.session_id = s.session_id AND agent_events.agent = ?))
      AND (? = '' OR s.label = ?)
      AND (? = 0 OR s.ended_at IS NULL OR EXISTS (
            SELECT 1
              FROM event_metadata_projection late_ev
             WHERE late_ev.session_id = s.session_id
               AND late_ev.created_at_norm > ts_norm(s.ended_at)
          ))
      AND (? = '' OR ts_norm(s.started_at) >= ts_norm(?))
      AND (? = '' OR ts_norm(s.started_at) < ts_norm(?))
    ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
    LIMIT ? OFFSET ?
  ),
  event_agg AS (
    SELECT
      e.session_id,
      COUNT(*) AS total_events,
      SUM(CASE WHEN e.kind = 'command_executed' THEN 1 ELSE 0 END) AS command_count,
      GROUP_CONCAT(DISTINCT e.agent) AS agents
    FROM filtered_sessions fs
    CROSS JOIN event_metadata_projection e ON e.session_id = fs.session_id
    GROUP BY e.session_id
  ),
  latest_events AS (
    SELECT session_id, id AS latest_event_id, created_at AS latest_event_at, kind AS latest_event_kind
    FROM (
      SELECT
        e.session_id,
        e.id,
        e.created_at,
        e.kind,
        ROW_NUMBER() OVER (
          PARTITION BY e.session_id
          ORDER BY e.created_at_norm DESC, e.id DESC
        ) AS rn
      FROM filtered_sessions fs
      CROSS JOIN event_metadata_projection e ON e.session_id = fs.session_id
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
  COALESCE(r.summary, '') AS summary,
  COALESCE(s.parent_session_id, '') AS parent_session_id,
  COALESCE(s.spawn_event_id, '') AS spawn_event_id,
  s.subagent_kind,
  s.spawn_order,
  COALESCE(s.model, '') AS model,
  COALESCE(latest.latest_event_kind, '') AS latest_event_kind,
  COALESCE(latest.latest_event_id, '') AS latest_event_id,
  -- New command_executed rows store an empty envelope body (#1675). Fall back to
  -- command_text only when it is known identity plaintext — never surface
  -- codec-managed bytes from SQL (checksum / size guards live in Go).
  COALESCE(
    NULLIF(latest_body.body, ''),
    CASE WHEN COALESCE(latest_audit.command_codec, 'identity') = 'identity'
         THEN latest_audit.command_text END,
    ''
  ) AS latest_event_body
FROM filtered_sessions s
LEFT JOIN session_refinements r ON r.session_id = s.session_id
LEFT JOIN event_agg agg ON agg.session_id = s.session_id
LEFT JOIN latest_events latest ON latest.session_id = s.session_id
LEFT JOIN events latest_body ON latest_body.id = latest.latest_event_id
LEFT JOIN command_audits latest_audit ON latest_audit.event_id = latest.latest_event_id
ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
