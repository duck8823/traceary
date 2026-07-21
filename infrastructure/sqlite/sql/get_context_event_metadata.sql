SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       ca.event_id, ca.exit_code, ca.failed
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE (? = '' OR e.workspace = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT ?
