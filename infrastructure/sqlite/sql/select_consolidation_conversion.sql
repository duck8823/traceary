SELECT client,
       COUNT(*)                                                                  AS requests,
       COUNT(DISTINCT session_id)                                                AS sessions_requested,
       SUM(CASE WHEN refine_outcome = 'accepted' THEN 1 ELSE 0 END)              AS requests_accepted,
       COUNT(DISTINCT CASE WHEN refine_outcome = 'accepted' THEN session_id END) AS sessions_refined
  FROM consolidation_requests
 WHERE requested_at >= ?
 GROUP BY client
 ORDER BY client;
