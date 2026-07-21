SELECT e.id,
       substr(e.body, 1, ?),
       e.body_stored_bytes,
       e.body_original_bytes,
       e.body_ingest_truncated,
       e.body_storage_truncated,
       e.created_at
  FROM events e
 WHERE e.kind = 'command_executed'
   AND (? = '' OR e.session_id = ?)
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT ?
