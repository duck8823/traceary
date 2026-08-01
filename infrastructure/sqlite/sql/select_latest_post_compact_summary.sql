WITH selected AS (
    SELECT m.id
      FROM event_metadata_projection m
     WHERE m.kind = 'compact_summary'
       AND m.session_id = ?
       AND (? = '' OR m.workspace = ?)
       AND COALESCE(m.source_hook, m.legacy_source_hook, '') <> 'pre_compact'
     ORDER BY m.created_at_norm DESC, m.id DESC
     LIMIT 1
)
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at
  FROM selected
  JOIN events e ON e.id = selected.id
