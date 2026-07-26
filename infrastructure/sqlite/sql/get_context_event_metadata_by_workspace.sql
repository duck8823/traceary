SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       e.command_audit_event_id, e.command_exit_code, e.command_failed
  FROM event_metadata_projection e
 WHERE e.workspace = ?
   AND (? = '' OR e.created_at_norm < ?)
   /* traceary:event-page-anchor */
 ORDER BY e.created_at_norm DESC, e.id DESC
 LIMIT ? OFFSET ?
