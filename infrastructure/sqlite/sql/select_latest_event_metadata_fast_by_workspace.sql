-- The common CLI path resolves the current repository as an implicit
-- workspace filter. Keep that filter inside the bounded latest-metadata
-- query; otherwise a normal invocation would silently fall back to the slow
-- general query. The workspace/created_at index or the created_at index can
-- locate the lexical latest candidate without reading event bodies.
WITH latest_lexical AS (
    SELECT created_at
      FROM events
     WHERE workspace = ?
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
 WHERE e.workspace = ?
   AND e.created_at >= s.second_prefix || '.'
   AND e.created_at <= s.second_prefix || 'Z'
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT 1
