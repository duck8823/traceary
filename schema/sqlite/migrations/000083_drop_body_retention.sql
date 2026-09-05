-- 000083_drop_body_retention.sql
--
-- Drops the body-discard machinery: body_availability, the retention
-- candidate index, raw_body_retention_* tables, and session_orphan_ranges.
-- Applied on a run-owned candidate, never at live open
-- (MigrationDataDependentOffline).
--
-- Triggers that reference body_availability are dropped first so
-- ALTER TABLE DROP COLUMN can rewrite events. They are recreated
-- without the availability gate: a body is what was written until
-- its row is removed.
--
-- Raises minimum_reader_version to 39 so older binaries fail loudly.
-- DROP COLUMN / DROP TABLE / DROP INDEX move pages to the freelist;
-- they do not shrink the file without VACUUM / candidate rewrite.

DROP TRIGGER IF EXISTS events_body_metadata_after_body_update;
DROP TRIGGER IF EXISTS event_metadata_projection_events_after_update;

DROP INDEX IF EXISTS idx_events_raw_body_retention_candidates;
DROP INDEX IF EXISTS idx_raw_body_retention_entries_event_id;

DROP TABLE IF EXISTS raw_body_retention_entries;
DROP TABLE IF EXISTS raw_body_retention_executions;
DROP TABLE IF EXISTS raw_body_retention_store_identity;
DROP TABLE IF EXISTS session_orphan_ranges;

ALTER TABLE events DROP COLUMN body_availability;

UPDATE store_format_state SET minimum_reader_version = 39 WHERE singleton = 1;

CREATE TRIGGER events_body_metadata_after_body_update
AFTER UPDATE OF body ON events
FOR EACH ROW
BEGIN
    UPDATE events
       SET body_stored_bytes = length(CAST(NEW.body AS BLOB))
     WHERE id = NEW.id;
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
             AND NEW.body LIKE '[phase:subagent]%'
            THEN 'subagent_stop'
            WHEN NEW.source_hook IS NULL
             AND NEW.kind = 'compact_summary'
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
        legacy_source_hook = CASE
            WHEN NEW.kind IS NOT OLD.kind
              OR NEW.source_hook IS NOT OLD.source_hook
              OR NEW.body IS NOT OLD.body
            THEN excluded.legacy_source_hook
            ELSE event_metadata_projection.legacy_source_hook
        END,
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
