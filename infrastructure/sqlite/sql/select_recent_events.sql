-- No source_hook filter: planner picks idx_events_created_at for the
-- ORDER BY. When a source_hook filter is set, the Go datasource
-- dispatches to select_recent_events_by_source_hook.sql instead so the
-- compound `(source_hook, created_at DESC, id DESC)` partial index can
-- cover the query without a post-filter scan. See #683.
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE (? = '' OR e.kind = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = 0 OR ca.failed = 1 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
   AND (? = '' OR e.created_at >= ?)
   AND (? = '' OR e.created_at < ?)
 ORDER BY e.created_at DESC, e.id DESC
 LIMIT ? OFFSET ?
