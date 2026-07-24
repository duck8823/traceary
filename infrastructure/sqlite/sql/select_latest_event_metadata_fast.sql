-- This bounded fast path is used only for an unfiltered first-page metadata
-- read (`limit=1, offset=0`). The existing created_at index locates the
-- newest timestamp-second without touching event bodies. RFC3339Nano's
-- variable fractional-width text form is not globally time-sortable, so the
-- outer query reorders only rows in that one second by ts_norm().
--
-- This keeps the historical boundary-correct timestamp contract while avoiding
-- a full-store ts_norm() sort for `traceary list --limit 1 --fields ts,kind`.
WITH latest_lexical AS (
    SELECT created_at
      FROM events
     ORDER BY created_at DESC, id DESC
     LIMIT 1
), latest_second AS (
    SELECT substr(created_at, 1, 19) AS second_prefix
      FROM latest_lexical
)
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       ca.event_id, ca.exit_code, ca.failed
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
  JOIN latest_second s
 WHERE e.created_at >= s.second_prefix || '.'
   AND e.created_at <= s.second_prefix || 'Z'
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT 1
