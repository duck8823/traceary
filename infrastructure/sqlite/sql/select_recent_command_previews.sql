-- Handoff recent-command summaries read command_audits.command_text.
-- events.body for command_executed is empty after #1675.
WITH selected AS (
    SELECT m.id,
           m.body_stored_bytes,
           m.body_original_bytes,
           m.body_ingest_truncated,
           m.body_storage_truncated,
           m.created_at,
           m.created_at_norm
      FROM event_metadata_projection m
     WHERE m.kind = 'command_executed'
       AND (? = '' OR m.session_id = ?)
     ORDER BY m.created_at_norm DESC, m.id DESC
     LIMIT ?
)
SELECT selected.id,
       substr(COALESCE(a.command_text, e.body), 1, ?),
       -- Prefer command_plaintext_bytes: once the payload codec is on,
       -- length(command_text) is the compressed (or BLOB) size, not the
       -- logical command size callers report as StoredBytes.
       COALESCE(a.command_plaintext_bytes, length(CAST(a.command_text AS BLOB)), selected.body_stored_bytes),
       selected.body_original_bytes,
       selected.body_ingest_truncated,
       selected.body_storage_truncated,
       selected.created_at
  FROM selected
  JOIN events e ON e.id = selected.id
  LEFT JOIN command_audits a ON a.event_id = selected.id
 ORDER BY selected.created_at_norm DESC, selected.id DESC
