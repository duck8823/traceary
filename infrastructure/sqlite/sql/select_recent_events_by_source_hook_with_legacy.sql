-- Source_hook filtered query: the primary branch uses
-- `e.source_hook = ?` as a top-level conjunct so the planner can serve
-- the WHERE + ORDER BY from the compound partial index
-- `idx_events_source_hook_time (source_hook, created_at DESC, id DESC)`.
-- A UNION ALL branch matches pre-#672 legacy rows via the body prefix
-- so migration-window data keeps working. See #683.
--
-- Result limit is applied to the combined set so pagination is stable
-- even when all hits come from the legacy branch.
SELECT id, kind, client, agent, session_id, workspace, body, source_hook, created_at
  FROM (
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
          FROM events e
          LEFT JOIN command_audits ca ON ca.event_id = e.id
         WHERE e.source_hook = ?
           AND (? = '' OR e.kind = ?)
           AND (? = '' OR e.client = ?)
           AND (? = '' OR e.agent = ?)
           AND (? = '' OR e.session_id = ?)
           AND (? = '' OR e.workspace = ?)
           AND (? = 0 OR ca.failed = 1 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
           AND (? = '' OR e.created_at >= ?)
           AND (? = '' OR e.created_at < ?)
        UNION ALL
        SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
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
           AND (? = '' OR e.created_at >= ?)
           AND (? = '' OR e.created_at < ?)
       ) events_union
 ORDER BY created_at DESC, id DESC
 LIMIT ? OFFSET ?
