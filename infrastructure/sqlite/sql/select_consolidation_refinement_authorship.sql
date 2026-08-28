SELECT r.client,
       COALESCE(sr.produced_by, '') AS produced_by,
       COUNT(*)                     AS sessions
  FROM (SELECT client, session_id, MIN(requested_at) AS first_requested_at
          FROM consolidation_requests
         WHERE requested_at >= ?
         GROUP BY client, session_id) AS r
  LEFT JOIN session_refinements AS sr
         ON sr.session_id = r.session_id
        AND ts_valid(sr.produced_at) = 1
        AND ts_norm(sr.produced_at) >= r.first_requested_at
 GROUP BY r.client, produced_by
 ORDER BY r.client, produced_by;
