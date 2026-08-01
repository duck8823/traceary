SELECT m.id, m.created_at_norm
  FROM event_metadata_projection m
 WHERE m.kind = 'compact_summary'
   AND m.session_id = ?
   AND COALESCE(m.source_hook, m.legacy_source_hook, '') <> 'pre_compact'
   AND (? = '' OR m.workspace = ?)
   AND (? = '' OR m.created_at_norm < ? OR (m.created_at_norm = ? AND m.id < ?))
 ORDER BY m.created_at_norm DESC, m.id DESC
 LIMIT ?
