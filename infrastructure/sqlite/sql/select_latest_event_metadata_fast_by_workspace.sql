-- The common CLI path resolves the current repository as an implicit
-- workspace filter. Keep that filter inside the bounded latest-metadata
-- query; otherwise a normal invocation would silently fall back to the slow
-- general query. The workspace/created_at index or the created_at index can
-- locate the lexical latest candidate without reading event bodies.
WITH latest_lexical AS (
    SELECT created_at
      FROM events
     WHERE workspace = ?
     -- Keep this order limited to the legacy `(workspace, created_at)` index.
     -- The timestamp alone selects the latest second; ties have the same
     -- created_at value, so id cannot affect the second that the outer query
     -- normalizes and orders. Adding id here forces SQLite to sort every row
     -- in a large workspace when that legacy index is present.
     ORDER BY created_at DESC
     LIMIT 1
), latest_second AS (
    SELECT substr(created_at, 1, 19) AS second_prefix
      FROM latest_lexical
), same_second_ids AS (
    -- Exact-second values sort after fractional RFC3339Nano text. Keep this
    -- equality probe separate from the fractional range so SQLite can retain
    -- a lower and upper created_at index bound for the latter.
    SELECT e.id
      FROM latest_second s
     CROSS JOIN events e
     WHERE e.workspace = ?
       AND e.created_at = s.second_prefix || 'Z'
    UNION ALL
    SELECT e.id
      FROM latest_second s
     CROSS JOIN events e
     WHERE e.workspace = ?
       AND e.created_at >= s.second_prefix || '.'
       AND e.created_at <= s.second_prefix || 'Z'
)
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       ca.event_id, ca.exit_code, ca.failed
  FROM same_second_ids candidate
  -- CROSS JOIN preserves candidate-first evaluation. A plain INNER JOIN lets
  -- SQLite reverse the join and scan all events before probing the tiny
  -- same-second candidate set.
 CROSS JOIN events e ON e.id = candidate.id
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT 1
