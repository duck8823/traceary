SELECT session_id, from_event_id, to_event_id, observed_at
  FROM session_orphan_ranges
 ORDER BY session_id ASC, to_event_id ASC
