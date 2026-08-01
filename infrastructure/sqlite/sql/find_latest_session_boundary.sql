WITH selected AS (
    SELECT started.id
      FROM event_metadata_projection boundary
      JOIN event_metadata_projection started
        ON started.session_id = boundary.session_id
       AND started.client = boundary.client
       AND started.agent = boundary.agent
       AND started.workspace = boundary.workspace
       AND started.kind = ?
     WHERE boundary.kind IN ('session_started', 'session_ended')
       AND (? = '' OR started.client = ?)
       AND (? = '' OR started.agent = ?)
       AND (? = '' OR started.workspace = ?)
       AND NOT EXISTS (
           SELECT 1 FROM event_metadata_projection newer_started
            WHERE newer_started.kind = ?
              AND newer_started.session_id = started.session_id
              AND newer_started.client = started.client
              AND newer_started.agent = started.agent
              AND newer_started.workspace = started.workspace
              AND (newer_started.created_at_norm > started.created_at_norm OR
                  (newer_started.created_at_norm = started.created_at_norm AND newer_started.id > started.id))
       )
     ORDER BY boundary.created_at_norm DESC, boundary.id DESC
     LIMIT 1
)
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.body, e.body_availability, e.source_hook, e.created_at
  FROM selected JOIN events e ON e.id = selected.id
