-- 000082_drop_encoded_payloads.sql
--
-- Decodes remaining encoded payloads on the run-owned candidate (Go
-- beforeApply), then drops codec metadata columns, rehearsal/backfill/
-- compatibility tables, and maximum_payload_format. Applied on a
-- candidate, never at live open (MigrationDataDependentOffline).
--
-- Triggers that reference dropped columns or tables are dropped first:
-- a surviving events/command_audits trigger would fail every later hook
-- write with "no such column" / "no such table". Codec-aware body
-- derivation and projection triggers are then recreated plaintext-only.
--
-- applyMigration wraps this body in one transaction with foreign_keys(1).
-- PRAGMA foreign_keys cannot be toggled mid-transaction, so the 12-step
-- table rebuild cannot run here. SQLite >= 3.35 DROP COLUMN rewrites the
-- table in place while preserving FKs, indexes, and unrelated triggers.
-- Indexes on events therefore stay; they are not dropped and recreated.
--
-- Raises minimum_reader_version to 38 so older binaries fail loudly.
-- DROP COLUMN / DROP TABLE move pages to the freelist; they do not
-- shrink the file without VACUUM / candidate rewrite.

DROP TRIGGER IF EXISTS payload_rehearsal_freeze_rows_insert;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_rows_update;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_rows_delete;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_events_update;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_events_insert;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_events_delete;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_audits_update;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_audits_insert;
DROP TRIGGER IF EXISTS payload_rehearsal_freeze_audits_delete;

DROP TRIGGER IF EXISTS payload_codec_events_insert;
DROP TRIGGER IF EXISTS payload_codec_events_update_guard;
DROP TRIGGER IF EXISTS payload_codec_events_update;
DROP TRIGGER IF EXISTS payload_codec_events_delete;
DROP TRIGGER IF EXISTS payload_codec_audits_insert;
DROP TRIGGER IF EXISTS payload_codec_audits_update_guard;
DROP TRIGGER IF EXISTS payload_codec_audits_update;
DROP TRIGGER IF EXISTS payload_codec_audits_delete;

DROP TRIGGER IF EXISTS events_body_metadata_after_insert;
DROP TRIGGER IF EXISTS events_body_metadata_after_body_update;
DROP TRIGGER IF EXISTS event_metadata_projection_events_after_insert;
DROP TRIGGER IF EXISTS event_metadata_projection_events_after_update;
DROP TRIGGER IF EXISTS event_metadata_projection_events_after_delete;
DROP TRIGGER IF EXISTS events_created_at_norm_after_insert;
DROP TRIGGER IF EXISTS events_created_at_norm_after_update;
DROP TRIGGER IF EXISTS events_id_immutable;
DROP TRIGGER IF EXISTS event_metadata_projection_command_audits_after_insert;
DROP TRIGGER IF EXISTS event_metadata_projection_command_audits_after_update;
DROP TRIGGER IF EXISTS event_metadata_projection_command_audits_after_delete;

DROP INDEX IF EXISTS idx_events_nonidentity_body_codec;
DROP INDEX IF EXISTS idx_command_audits_nonidentity_command_codec;
DROP INDEX IF EXISTS idx_command_audits_nonidentity_input_codec;
DROP INDEX IF EXISTS idx_command_audits_nonidentity_output_codec;

ALTER TABLE events DROP COLUMN body_codec;
ALTER TABLE events DROP COLUMN body_format_version;
ALTER TABLE events DROP COLUMN body_plaintext_bytes;
ALTER TABLE events DROP COLUMN body_encoded_bytes;
ALTER TABLE events DROP COLUMN body_sha256;

ALTER TABLE command_audits DROP COLUMN command_codec;
ALTER TABLE command_audits DROP COLUMN command_format_version;
ALTER TABLE command_audits DROP COLUMN command_plaintext_bytes;
ALTER TABLE command_audits DROP COLUMN command_encoded_bytes;
ALTER TABLE command_audits DROP COLUMN command_sha256;
ALTER TABLE command_audits DROP COLUMN input_codec;
ALTER TABLE command_audits DROP COLUMN input_format_version;
ALTER TABLE command_audits DROP COLUMN input_plaintext_bytes;
ALTER TABLE command_audits DROP COLUMN input_encoded_bytes;
ALTER TABLE command_audits DROP COLUMN input_sha256;
ALTER TABLE command_audits DROP COLUMN output_codec;
ALTER TABLE command_audits DROP COLUMN output_format_version;
ALTER TABLE command_audits DROP COLUMN output_plaintext_bytes;
ALTER TABLE command_audits DROP COLUMN output_encoded_bytes;
ALTER TABLE command_audits DROP COLUMN output_sha256;

DROP TABLE IF EXISTS payload_rehearsal_rows;
DROP TABLE IF EXISTS payload_rehearsal_checkpoints;
DROP TABLE IF EXISTS payload_rehearsal_runs;
DROP TABLE IF EXISTS payload_backfill_runs;
DROP TABLE IF EXISTS payload_codec_compatibility_state;

ALTER TABLE store_format_state DROP COLUMN maximum_payload_format;
UPDATE store_format_state SET minimum_reader_version = 38 WHERE singleton = 1;

CREATE TRIGGER events_body_metadata_after_insert
AFTER INSERT ON events
FOR EACH ROW
BEGIN
    UPDATE events
       SET body_stored_bytes = length(CAST(NEW.body AS BLOB))
     WHERE id = NEW.id;
END;

CREATE TRIGGER events_body_metadata_after_body_update
AFTER UPDATE OF body ON events
FOR EACH ROW
WHEN NEW.body_availability = 'available'
BEGIN
    UPDATE events
       SET body_stored_bytes = length(CAST(NEW.body AS BLOB))
     WHERE id = NEW.id;
END;

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
        legacy_source_hook = excluded.legacy_source_hook,
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
             AND NEW.body_availability = 'available'
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
             AND NEW.body_availability = 'available'
            THEN excluded.body_stored_bytes
            ELSE NEW.body_stored_bytes
        END,
        body_ingest_truncated = excluded.body_ingest_truncated,
        body_storage_truncated = excluded.body_storage_truncated,
        body_metadata_version = excluded.body_metadata_version;
END;

CREATE TRIGGER event_metadata_projection_events_after_delete
AFTER DELETE ON events
FOR EACH ROW
BEGIN
    DELETE FROM event_metadata_projection WHERE id = OLD.id;
END;

CREATE TRIGGER events_created_at_norm_after_insert
AFTER INSERT ON events
FOR EACH ROW
WHEN NEW.created_at_norm IS NULL OR NEW.created_at_norm != CASE
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
END
BEGIN
    UPDATE events
       SET created_at_norm = CASE
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
       END
     WHERE id = NEW.id;
END;

CREATE TRIGGER events_created_at_norm_after_update
AFTER UPDATE OF created_at ON events
FOR EACH ROW
WHEN NEW.created_at_norm IS NULL OR NEW.created_at_norm != CASE
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
END
BEGIN
    UPDATE events
       SET created_at_norm = CASE
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
       END
     WHERE id = NEW.id;
END;

CREATE TRIGGER events_id_immutable BEFORE UPDATE OF id ON events
BEGIN
 SELECT RAISE(ABORT, 'events.id is immutable');
END;

CREATE TRIGGER event_metadata_projection_command_audits_after_insert
AFTER INSERT ON command_audits
FOR EACH ROW
BEGIN
    UPDATE event_metadata_projection
       SET command_audit_event_id = NEW.event_id,
           command_exit_code = NEW.exit_code,
           command_failed = NEW.failed
     WHERE id = NEW.event_id;
END;

CREATE TRIGGER event_metadata_projection_command_audits_after_update
AFTER UPDATE OF event_id, exit_code, failed ON command_audits
FOR EACH ROW
BEGIN
    UPDATE event_metadata_projection
       SET command_audit_event_id = NULL,
           command_exit_code = NULL,
           command_failed = NULL
     WHERE id = OLD.event_id
       AND OLD.event_id IS NOT NEW.event_id;

    UPDATE event_metadata_projection
       SET command_audit_event_id = NEW.event_id,
           command_exit_code = NEW.exit_code,
           command_failed = NEW.failed
     WHERE id = NEW.event_id;
END;

CREATE TRIGGER event_metadata_projection_command_audits_after_delete
AFTER DELETE ON command_audits
FOR EACH ROW
BEGIN
    UPDATE event_metadata_projection
       SET command_audit_event_id = NULL,
           command_exit_code = NULL,
           command_failed = NULL
     WHERE id = OLD.event_id;
END;
