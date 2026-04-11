WITH candidate_sessions AS (
     SELECT started.id,
            started.kind,
            started.client,
            started.agent,
            started.session_id,
            started.workspace,
            started.body,
            started.created_at,
            (
              SELECT boundary.created_at
                FROM events boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY boundary.created_at DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_created_at,
            (
              SELECT boundary.id
                FROM events boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY boundary.created_at DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_id
       FROM events started
      WHERE started.kind = ?
        AND (? = '' OR started.client = ?)
        AND (? = '' OR started.agent = ?)
        AND (? = '' OR started.workspace = ?)
        AND NOT EXISTS (
             SELECT 1
               FROM events newer_started
              WHERE newer_started.kind = ?
                AND newer_started.session_id = started.session_id
                AND newer_started.client = started.client
                AND newer_started.agent = started.agent
                AND newer_started.workspace = started.workspace
                AND (
                     newer_started.created_at > started.created_at OR
                     (newer_started.created_at = started.created_at AND newer_started.id > started.id)
                )
        )
        AND (
             ? = 0 OR NOT EXISTS (
                 SELECT 1
                   FROM events ended
                  WHERE ended.kind = ?
                    AND ended.session_id = started.session_id
                    AND ended.client = started.client
                    AND ended.agent = started.agent
                    AND ended.workspace = started.workspace
                    AND (
                         ended.created_at > started.created_at OR
                         (ended.created_at = started.created_at AND ended.id > started.id)
                    )
             )
        )
)
SELECT id,
       kind,
       client,
       agent,
       session_id,
       workspace,
       body,
       created_at
  FROM candidate_sessions
 ORDER BY CASE WHEN ? THEN created_at ELSE latest_boundary_created_at END DESC,
          CASE WHEN ? THEN id ELSE latest_boundary_id END DESC
 LIMIT 1
