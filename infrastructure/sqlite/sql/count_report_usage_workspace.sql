SELECT
  COALESCE(SUM(CASE WHEN observation.accounting = 'excluded' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN observation.input_state = 'unavailable' OR observation.total_state = 'unavailable' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN observation.host = 'kimi' AND observation.source_name = 'main_wire' AND observation.accounting = 'excluded' AND observation.input_state = 'known' THEN 1 ELSE 0 END), 0)
  FROM usage_observations AS observation
  LEFT JOIN sessions AS session
    ON session.session_id = observation.session_id
 WHERE observation.status = 'finalized'
   AND (? = '' OR session.workspace = ?)
   AND (? = '' OR session.client = ?)
