-- Source_hook filtered query: the primary branch uses
-- `e.source_hook = ?` as a top-level conjunct so hook-specific filtering stays
-- out of the general recent-events path.
-- A UNION ALL branch matches pre-#672 legacy rows via event_metadata_projection
-- .legacy_source_hook so migration-window data keeps working after bodies are
-- encoded as opaque BLOBs. See #683 and #1685 D6.
--
-- Result limit is applied to the combined set so pagination is stable
-- even when all hits come from the legacy branch.
SELECT id, kind, client, agent, session_id, workspace, body, body_availability, source_hook, created_at,
       command_wrapper, command_name,
       input_truncated, output_truncated,
       input_original_bytes, output_original_bytes, exit_code, failed, failure_reason, output_metadata
  FROM (
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at,
               ca.command_wrapper, ca.command_name,
               ca.input_truncated, ca.output_truncated,
               ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason, ca.output_metadata
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
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at,
               ca.command_wrapper, ca.command_name,
               ca.input_truncated, ca.output_truncated,
               ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason, ca.output_metadata
          FROM events e
          LEFT JOIN command_audits ca ON ca.event_id = e.id
          LEFT JOIN event_metadata_projection emp ON emp.id = e.id
         WHERE e.source_hook IS NULL
           AND (
                 (? = 'subagent_stop' AND emp.legacy_source_hook = 'subagent_stop')
              OR (? = 'pre_compact' AND emp.legacy_source_hook = 'pre_compact')
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
