WITH candidate_sessions AS (
     SELECT started.id,
            started.session_id,
            started.client,
            started.agent,
            started.workspace,
            started.created_at_norm,
            (
              SELECT boundary.created_at_norm
                FROM event_metadata_projection boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY boundary.created_at_norm DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_created_at_norm,
            (
              SELECT boundary.id
                FROM event_metadata_projection boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY boundary.created_at_norm DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_id
       FROM event_metadata_projection started
      WHERE started.kind = ?
        AND (? = '' OR started.client = ?)
        AND (? = '' OR started.agent = ?)
        AND (? = '' OR started.workspace = ?)
        AND NOT EXISTS (
             SELECT 1
               FROM event_metadata_projection newer_started
              WHERE newer_started.kind = ?
                AND newer_started.session_id = started.session_id
                AND newer_started.client = started.client
                AND newer_started.agent = started.agent
                AND newer_started.workspace = started.workspace
                AND (
                     newer_started.created_at_norm > started.created_at_norm OR
                     (newer_started.created_at_norm = started.created_at_norm AND newer_started.id > started.id)
                )
        )
        AND (
             ? = 0 OR (
                 NOT EXISTS (
                     SELECT 1
                       FROM event_metadata_projection ended
                      WHERE ended.kind = ?
                        AND ended.session_id = started.session_id
                        AND ended.client = started.client
                        AND ended.agent = started.agent
                        AND ended.workspace = started.workspace
                        AND (
                             ended.created_at_norm > started.created_at_norm OR
                             (ended.created_at_norm = started.created_at_norm AND ended.id > started.id)
                        )
                        AND NOT EXISTS (
                             SELECT 1
                               FROM event_metadata_projection later_ev
                              WHERE later_ev.session_id = started.session_id
                                AND (
                                     later_ev.created_at_norm > ended.created_at_norm OR
                                     (later_ev.created_at_norm = ended.created_at_norm AND later_ev.id > ended.id)
                                )
                        )
                 )
                 AND NOT EXISTS (
                     SELECT 1
                       FROM sessions ended_row
                      WHERE ended_row.session_id = started.session_id
                        AND ended_row.ended_at IS NOT NULL
                        AND NOT EXISTS (
                             SELECT 1
                               FROM event_metadata_projection row_later
                              WHERE row_later.session_id = started.session_id
                                AND row_later.created_at_norm > ts_norm(ended_row.ended_at)
                        )
                 )
             )
        )
), selected AS (
    SELECT id
      FROM candidate_sessions
     ORDER BY CASE WHEN ? THEN created_at_norm ELSE latest_boundary_created_at_norm END DESC,
              CASE WHEN ? THEN id ELSE latest_boundary_id END DESC
     LIMIT 1
)
SELECT e.id,
       e.kind,
       e.client,
       e.agent,
       e.session_id,
       e.workspace,
       e.body,
       e.body_availability,
       e.source_hook,
       e.created_at
  FROM selected
  JOIN events e ON e.id = selected.id
