-- 000086_drop_legacy_source_hook.sql
--
-- Drops event_metadata_projection.legacy_source_hook (and its inline
-- CHECK) after Go refuses when any non-NULL value remains. Classified
-- MigrationDataDependentOffline. Applied on a run-owned candidate,
-- never at live open (empty-store inline bootstrap may apply this SQL
-- only when every remaining value is NULL).
--
-- SQLite aborts ALTER TABLE DROP COLUMN while any trigger references
-- the column ('error in trigger ... after drop column: no such column:
-- excluded.legacy_source_hook'). Drop both referencing triggers first,
-- then DROP COLUMN, then recreate them without the column. Only these
-- two names ever referenced the column in the migration history
-- (034/053/056/082/083). Primary precedent: 000083 (drop-then-recreate).
-- Supporting: 000082 (plaintext-only trigger bodies).
--
-- search_projection_inventory_compat was dropped by 080;
-- payload_codec_compatibility_state and maximum_payload_format by 082.
-- session_workspace_aliases stays: named readers still require it.
--
-- Raises minimum_reader_version to 42 so older binaries fail loudly.
-- DROP COLUMN moves pages to the freelist; it does not shrink the file
-- without VACUUM / candidate rewrite.

DROP TRIGGER IF EXISTS event_metadata_projection_events_after_insert;
DROP TRIGGER IF EXISTS event_metadata_projection_events_after_update;

ALTER TABLE event_metadata_projection DROP COLUMN legacy_source_hook;

UPDATE store_format_state SET minimum_reader_version = 42 WHERE singleton = 1;

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
        length(CAST(NEW.body AS BLOB)),
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
        created_at = excluded.created_at,
        created_at_norm = excluded.created_at_norm,
        body_original_bytes = excluded.body_original_bytes,
        body_stored_bytes = excluded.body_stored_bytes,
        body_ingest_truncated = excluded.body_ingest_truncated,
        body_storage_truncated = excluded.body_storage_truncated,
        body_metadata_version = excluded.body_metadata_version;
END;

CREATE TRIGGER event_metadata_projection_events_after_update
AFTER UPDATE OF
    id,
    kind,
    client,
    agent,
    session_id,
    workspace,
    source_hook,
    created_at,
    created_at_norm,
    body,
    body_original_bytes,
    body_stored_bytes,
    body_ingest_truncated,
    body_storage_truncated,
    body_metadata_version
ON events
FOR EACH ROW
BEGIN
    DELETE FROM event_metadata_projection
     WHERE id = OLD.id
       AND OLD.id IS NOT NEW.id;

    INSERT INTO event_metadata_projection (
        id,
        kind,
        client,
        agent,
        session_id,
        workspace,
        source_hook,
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
            WHEN NEW.body IS NOT OLD.body
            THEN length(CAST(NEW.body AS BLOB))
            ELSE NEW.body_stored_bytes
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
        created_at = excluded.created_at,
        created_at_norm = CASE
            WHEN NEW.created_at IS NOT OLD.created_at
            THEN excluded.created_at_norm
            ELSE COALESCE(NEW.created_at_norm, event_metadata_projection.created_at_norm)
        END,
        body_original_bytes = excluded.body_original_bytes,
        body_stored_bytes = CASE
            WHEN NEW.body IS NOT OLD.body
            THEN excluded.body_stored_bytes
            ELSE NEW.body_stored_bytes
        END,
        body_ingest_truncated = excluded.body_ingest_truncated,
        body_storage_truncated = excluded.body_storage_truncated,
        body_metadata_version = excluded.body_metadata_version;
END;
