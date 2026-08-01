WITH selected AS (
    SELECT m.id,
           m.body_stored_bytes,
           m.body_original_bytes,
           m.body_ingest_truncated,
           m.body_storage_truncated,
           m.created_at,
           m.created_at_norm
      FROM event_metadata_projection m
     WHERE m.kind = 'command_executed'
       AND (? = '' OR m.session_id = ?)
     ORDER BY m.created_at_norm DESC, m.id DESC
     LIMIT ?
)
SELECT selected.id,
       substr(e.body, 1, ?),
       selected.body_stored_bytes,
       selected.body_original_bytes,
       selected.body_ingest_truncated,
       selected.body_storage_truncated,
       selected.created_at
  FROM selected
  JOIN events e ON e.id = selected.id
 ORDER BY selected.created_at_norm DESC, selected.id DESC
