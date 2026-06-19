-- No source_hook filter. When a source_hook filter is set, the Go datasource
-- dispatches to select_recent_events_by_source_hook.sql so hook-specific
-- predicates stay isolated from the general recent-events path. See #683.
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE (? = '' OR e.kind = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.workspace = ?)
   AND (? = 0 OR ca.failed = 1 OR (ca.exit_code IS NOT NULL AND ca.exit_code != 0))
   -- created_at is variable-width RFC3339Nano; ts_norm() makes the period
   -- bound and ordering boundary-correct (#1185).
   AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
   AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT ? OFFSET ?
