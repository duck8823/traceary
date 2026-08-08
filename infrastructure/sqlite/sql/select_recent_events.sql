-- No source_hook filter. When a source_hook filter is set, the Go datasource
-- dispatches to select_recent_events_by_source_hook.sql so hook-specific
-- predicates stay isolated from the general recent-events path. See #683.
--
-- command_audits columns are selected in the same row so readers can use the
-- retained execution record without a per-event follow-up query (#1675).
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at,
       ca.command_text, ca.command_wrapper, ca.command_name,
       ca.input_text, ca.output_text, ca.input_truncated, ca.output_truncated,
       ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason
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
