SELECT e.kind, e.created_at
  FROM event_metadata_projection e
 ORDER BY e.created_at_norm DESC, e.id DESC
 LIMIT 1
