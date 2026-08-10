-- Make INSERT body derivations codec-aware, matching migration 053's UPDATE
-- guards. A non-identity body is opaque stored data: preserve its plaintext
-- provenance and do not infer legacy hooks from compressed bytes.
-- Constant cost: DROP + CREATE only; no table rewrite or data scan.

DROP TRIGGER IF EXISTS events_body_metadata_after_insert;
CREATE TRIGGER events_body_metadata_after_insert
AFTER INSERT ON events
FOR EACH ROW
BEGIN
    UPDATE events
       SET body_stored_bytes = CASE
           WHEN NEW.body_codec IS NULL OR NEW.body_codec = 'identity'
           THEN length(CAST(NEW.body AS BLOB))
           ELSE COALESCE(NEW.body_stored_bytes, NEW.body_plaintext_bytes)
       END
     WHERE id = NEW.id;
END;

DROP TRIGGER IF EXISTS event_metadata_projection_events_after_insert;
CREATE TRIGGER event_metadata_projection_events_after_insert
AFTER INSERT ON events
FOR EACH ROW
BEGIN
    INSERT INTO event_metadata_projection (
        id,
        kind,
        client,
        agent,
        session_id,
        workspace,
        source_hook,
        legacy_source_hook,
        created_at,
        created_at_norm,
        body_original_bytes,
        body_stored_bytes,
        body_ingest_truncated,
        body_storage_truncated,
        body_metadata_version,
        command_audit_event_id,
        command_exit_code,
        command_failed
    ) VALUES (
        NEW.id,
        NEW.kind,
        NEW.client,
        NEW.agent,
        NEW.session_id,
        NEW.workspace,
        NEW.source_hook,
        CASE
            WHEN NEW.source_hook IS NULL
             AND NEW.kind = 'session_ended'
             AND (NEW.body_codec IS NULL OR NEW.body_codec = 'identity')
             AND NEW.body LIKE '[phase:subagent]%'
            THEN 'subagent_stop'
            WHEN NEW.source_hook IS NULL
             AND NEW.kind = 'compact_summary'
             AND (NEW.body_codec IS NULL OR NEW.body_codec = 'identity')
             AND NEW.body LIKE '[phase:pre-compact]%'
            THEN 'pre_compact'
            ELSE NULL
        END,
        NEW.created_at,
        CASE
            WHEN substr(NEW.created_at, -1) = 'Z' AND length(NEW.created_at) >= 20
            THEN substr(NEW.created_at, 1, 19) || '.' ||
                 substr(
                     CASE
                         WHEN substr(NEW.created_at, 20, 1) = '.'
                         THEN substr(NEW.created_at, 21, length(NEW.created_at) - 21)
                         ELSE ''
                     END || '000000000',
                     1, 9
                 ) || 'Z'
            ELSE NEW.created_at
        END,
        NEW.body_original_bytes,
        CASE
            WHEN NEW.body_codec IS NULL OR NEW.body_codec = 'identity'
            THEN length(CAST(NEW.body AS BLOB))
            ELSE COALESCE(NEW.body_stored_bytes, NEW.body_plaintext_bytes)
        END,
        NEW.body_ingest_truncated,
        NEW.body_storage_truncated,
        NEW.body_metadata_version,
        NULL,
        NULL,
        NULL
    )
    ON CONFLICT(id) DO UPDATE SET
        kind = excluded.kind,
        client = excluded.client,
        agent = excluded.agent,
        session_id = excluded.session_id,
        workspace = excluded.workspace,
        source_hook = excluded.source_hook,
        legacy_source_hook = excluded.legacy_source_hook,
        created_at = excluded.created_at,
        created_at_norm = excluded.created_at_norm,
        body_original_bytes = excluded.body_original_bytes,
        body_stored_bytes = excluded.body_stored_bytes,
        body_ingest_truncated = excluded.body_ingest_truncated,
        body_storage_truncated = excluded.body_storage_truncated,
        body_metadata_version = excluded.body_metadata_version;
END;
