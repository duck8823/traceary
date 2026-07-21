SELECT id, kind, client, agent, session_id, workspace,
       source_hook, created_at,
       body_original_bytes, body_stored_bytes,
       body_ingest_truncated, body_storage_truncated,
       body_metadata_version,
       audit_event_id, exit_code, failed
  FROM (
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
               e.source_hook, e.created_at,
               e.body_original_bytes, e.body_stored_bytes,
               e.body_ingest_truncated, e.body_storage_truncated,
               e.body_metadata_version,
               ca.event_id AS audit_event_id, ca.exit_code, ca.failed
          FROM events e
          LEFT JOIN command_audits ca ON ca.event_id = e.id
         WHERE e.source_hook = ?
           AND (? = '' OR e.kind = ?)
           AND (? = '' OR e.client = ?)
           AND (? = '' OR e.agent = ?)
           AND (? = '' OR e.session_id = ?)
           AND (? = '' OR e.workspace = ?)
           AND (? = 0 OR ca.failed = 1 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
           AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
           AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
        UNION ALL
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
               e.source_hook, e.created_at,
               e.body_original_bytes, e.body_stored_bytes,
               e.body_ingest_truncated, e.body_storage_truncated,
               e.body_metadata_version,
               ca.event_id AS audit_event_id, ca.exit_code, ca.failed
          FROM events e
          LEFT JOIN command_audits ca ON ca.event_id = e.id
         WHERE e.source_hook IS NULL
           AND (
                 (? = 'subagent_stop' AND e.kind = 'session_ended'
                      AND e.body LIKE '[phase:subagent]%')
              OR (? = 'pre_compact' AND e.kind = 'compact_summary'
                      AND e.body LIKE '[phase:pre-compact]%')
               )
           AND (? = '' OR e.kind = ?)
           AND (? = '' OR e.client = ?)
           AND (? = '' OR e.agent = ?)
           AND (? = '' OR e.session_id = ?)
           AND (? = '' OR e.workspace = ?)
           AND (? = 0 OR ca.failed = 1 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
           AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
           AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
       ) events_union
 ORDER BY ts_norm(created_at) DESC, id DESC
 LIMIT ? OFFSET ?
