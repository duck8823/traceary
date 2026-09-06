SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       e.command_audit_event_id, e.command_exit_code, e.command_failed
  FROM event_metadata_projection e
 WHERE e.source_hook = ?
   AND (? = '' OR e.kind = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = 0 OR e.command_failed = 1 OR (e.command_exit_code IS NOT NULL AND e.command_exit_code != 0))
   AND (? = '' OR e.created_at_norm >= ?)
   AND (? = '' OR e.created_at_norm < ?)
   /* traceary:event-page-anchor */
 ORDER BY e.created_at_norm DESC, e.id DESC
 LIMIT ? OFFSET ?
