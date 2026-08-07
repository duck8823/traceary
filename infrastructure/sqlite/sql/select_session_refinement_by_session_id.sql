SELECT session_id, generation, covers_from_event_id, covers_to_event_id,
       summary, keywords, produced_by, produced_at, degraded
  FROM session_refinements
 WHERE session_id = ?
