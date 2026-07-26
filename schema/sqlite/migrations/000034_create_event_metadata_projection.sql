-- Keep body-free event reads off the body-bearing events table. The projection
-- contains every field required by EventMetadata, including the fixed legacy
-- hook classification and optional command-audit facts, but no event body or
-- command payload.
CREATE TABLE event_metadata_projection (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    client TEXT NOT NULL,
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    source_hook TEXT,
    legacy_source_hook TEXT
        CHECK (legacy_source_hook IS NULL OR legacy_source_hook IN ('subagent_stop', 'pre_compact')),
    created_at TEXT NOT NULL,
    created_at_norm TEXT,
    body_original_bytes INTEGER
        CHECK (body_original_bytes IS NULL OR body_original_bytes >= 0),
    body_stored_bytes INTEGER
        CHECK (body_stored_bytes IS NULL OR body_stored_bytes >= 0),
    body_ingest_truncated INTEGER
        CHECK (body_ingest_truncated IS NULL OR body_ingest_truncated IN (0, 1)),
    body_storage_truncated INTEGER
        CHECK (body_storage_truncated IS NULL OR body_storage_truncated IN (0, 1)),
    body_metadata_version INTEGER
        CHECK (body_metadata_version IS NULL OR body_metadata_version >= 0),
    command_audit_event_id TEXT,
    command_exit_code INTEGER,
    command_failed INTEGER
        CHECK (command_failed IS NULL OR command_failed IN (0, 1)),
    CHECK (
        (command_audit_event_id IS NULL AND command_exit_code IS NULL AND command_failed IS NULL)
        OR
        (command_audit_event_id = id AND command_failed IS NOT NULL)
    )
);

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
)
SELECT
    e.id,
    e.kind,
    e.client,
    e.agent,
    e.session_id,
    e.workspace,
    e.source_hook,
    CASE
        WHEN e.source_hook IS NULL
         AND e.kind = 'session_ended'
         AND e.body LIKE '[phase:subagent]%'
        THEN 'subagent_stop'
        WHEN e.source_hook IS NULL
         AND e.kind = 'compact_summary'
         AND e.body LIKE '[phase:pre-compact]%'
        THEN 'pre_compact'
        ELSE NULL
    END,
    e.created_at,
    e.created_at_norm,
    e.body_original_bytes,
    e.body_stored_bytes,
    e.body_ingest_truncated,
    e.body_storage_truncated,
    e.body_metadata_version,
    ca.event_id,
    ca.exit_code,
    ca.failed
FROM events e
LEFT JOIN command_audits ca ON ca.event_id = e.id;

CREATE INDEX idx_event_metadata_created_at_norm_id_desc
    ON event_metadata_projection(created_at_norm DESC, id DESC);
CREATE INDEX idx_event_metadata_workspace_created_at_norm_id_desc
    ON event_metadata_projection(workspace, created_at_norm DESC, id DESC);
CREATE INDEX idx_event_metadata_session_created_at_norm_id_desc
    ON event_metadata_projection(session_id, created_at_norm DESC, id DESC);
CREATE INDEX idx_event_metadata_workspace_session_created_at_norm_id_desc
    ON event_metadata_projection(workspace, session_id, created_at_norm DESC, id DESC);
CREATE INDEX idx_event_metadata_source_hook_created_at_norm_id_desc
    ON event_metadata_projection(source_hook, created_at_norm DESC, id DESC)
    WHERE source_hook IS NOT NULL;

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

CREATE TRIGGER event_metadata_projection_events_after_delete
AFTER DELETE ON events
FOR EACH ROW
BEGIN
    DELETE FROM event_metadata_projection WHERE id = OLD.id;
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
