-- Source_hook filtered query: the primary branch uses
-- `e.source_hook = ?` as a top-level conjunct so hook-specific filtering stays
-- out of the general recent-events path.
-- A UNION ALL branch matches pre-#672 legacy rows via the body prefix
-- so migration-window data keeps working. See #683.
--
-- Result limit is applied to the combined set so pagination is stable
-- even when all hits come from the legacy branch.
SELECT id, kind, client, agent, session_id, workspace, body, body_availability, source_hook, created_at,
       command_wrapper, command_name,
       input_truncated, output_truncated,
       input_original_bytes, output_original_bytes, exit_code, failed, failure_reason
  FROM (
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at,
               ca.command_wrapper, ca.command_name,
               ca.input_truncated, ca.output_truncated,
               ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason
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
               ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason
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
