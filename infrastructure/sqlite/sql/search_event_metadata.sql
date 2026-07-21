SELECT DISTINCT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       a.event_id, a.exit_code, a.failed
  FROM events e
  LEFT JOIN command_audits a ON a.event_id = e.id
 WHERE (? = '' OR
        (CASE WHEN json_valid(e.body)
                   AND json_type(e.body, '$.blocks') = 'array'
                   AND NOT EXISTS (
                     SELECT 1
                       FROM json_each(json_extract(e.body, '$.blocks'))
                      WHERE typeof(json_extract(value, '$.type')) != 'text'
                         OR typeof(json_extract(value, '$.text')) != 'text'
                   )
              THEN COALESCE(
                     (SELECT group_concat(json_extract(value, '$.text'), X'0A0A')
                        FROM json_each(json_extract(e.body, '$.blocks'))
                       WHERE json_extract(value, '$.type') = 'text'),
                     '')
              ELSE e.body
         END) LIKE ? ESCAPE '\' OR
        COALESCE(a.command_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.input_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.output_text, '') LIKE ? ESCAPE '\')
   AND (? = '' OR e.workspace = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.kind = ?)
   AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
   AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
   AND (? = 0 OR a.failed = 1 OR (a.exit_code IS NOT NULL AND a.exit_code != 0))
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT ? OFFSET ?
