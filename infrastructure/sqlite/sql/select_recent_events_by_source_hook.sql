-- Source_hook filtered query (primary-only path). `e.source_hook = ?`
-- is the top-level conjunct so hook-specific filtering stays out of the
-- general recent-events path.
--
-- Used for hook names that have no legacy body-prefix equivalent
-- (everything except subagent_stop / pre_compact). For those two names
-- the datasource dispatches to
-- `select_recent_events_by_source_hook_with_legacy.sql` instead so
-- pre-#672 rows that lack source_hook but carry the `[phase:*]` body
-- prefix stay reachable. See #683.
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.body_availability, e.source_hook, e.created_at
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE e.source_hook = ?
   AND (? = '' OR e.kind = ?)
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
