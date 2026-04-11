WITH candidate_sessions AS (
     SELECT started.id,
            started.kind,
            started.client,
            started.agent,
            started.session_id,
            started.repo,
            started.body,
            started.created_at,
            (
              SELECT boundary.created_at
                FROM events boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.repo = started.repo
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
                 AND boundary.repo = started.repo
                 AND boundary.kind IN (?, ?)
               ORDER BY boundary.created_at DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_id
       FROM events started
      WHERE started.kind = ?
        AND (? = '' OR started.client = ?)
        AND (? = '' OR started.agent = ?)
        AND (? = '' OR started.repo = ?)
        AND NOT EXISTS (
             SELECT 1
               FROM events newer_started
              WHERE newer_started.kind = ?
                AND newer_started.session_id = started.session_id
                AND newer_started.client = started.client
                AND newer_started.agent = started.agent
                AND newer_started.repo = started.repo
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
                    AND ended.repo = started.repo
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
       repo,
       body,
       created_at
  FROM candidate_sessions
 ORDER BY CASE WHEN ? THEN created_at ELSE latest_boundary_created_at END DESC,
          CASE WHEN ? THEN id ELSE latest_boundary_id END DESC
 LIMIT 1
