-- Add a stable search-document projection and an FTS5 trigram index without
-- scanning historical event bodies during migration. Historical rows are
-- filled by the resumable application backfill in small TEXT-primary-key
-- batches. events.rowid is intentionally not persisted because VACUUM may
-- rewrite it.
CREATE TABLE event_search_documents (
    search_document_id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,
    body_text TEXT NOT NULL DEFAULT '',
    command_text TEXT NOT NULL DEFAULT '',
    input_text TEXT NOT NULL DEFAULT '',
    output_text TEXT NOT NULL DEFAULT ''
);

CREATE VIRTUAL TABLE event_search_fts USING fts5(
    body_text,
    command_text,
    input_text,
    output_text,
    content='event_search_documents',
    content_rowid='search_document_id',
    tokenize='trigram case_sensitive 1'
);

-- SQLite lower() folds ASCII only in the bundled build. Lowering both the
-- projection and the FTS query therefore preserves the legacy LIKE behavior:
-- ASCII is case-insensitive while non-ASCII remains case-sensitive.
--
-- A canonical transcript envelope contributes text blocks only. Foreign JSON
-- and legacy plain text remain raw-searchable. Retention-unavailable bodies
-- contribute no body text.
CREATE VIEW event_search_projection AS
SELECT
    e.id AS event_id,
    lower(
        CASE
            WHEN e.body_availability = 'unavailable_retention' THEN ''
            WHEN json_valid(e.body)
                 AND json_type(e.body, '$') = 'object'
                 AND json_type(e.body, '$.blocks') = 'array'
                 AND NOT EXISTS (
                     SELECT 1
                       FROM json_each(json_extract(e.body, '$.blocks')) AS block
                      WHERE CASE
                          WHEN block.type != 'object' THEN 1
                          WHEN json_type(block.value, '$.type') IS NOT 'text' THEN 1
                          WHEN json_extract(block.value, '$.type') IN ('text', 'thinking')
                               AND json_type(block.value, '$.text') IS NOT 'text'
                          THEN 1
                          ELSE 0
                      END = 1
                 )
            THEN COALESCE(
                (
                    SELECT group_concat(json_extract(value, '$.text'), X'0A0A')
                      FROM json_each(json_extract(e.body, '$.blocks'))
                     WHERE json_extract(value, '$.type') = 'text'
                       AND json_type(value, '$.text') = 'text'
                ),
                ''
            )
            ELSE e.body
        END
    ) AS body_text,
    lower(COALESCE(a.command_text, '')) AS command_text,
    lower(COALESCE(a.input_text, '')) AS input_text,
    lower(COALESCE(a.output_text, '')) AS output_text
FROM events e
LEFT JOIN command_audits a ON a.event_id = e.id;

-- Standard external-content FTS maintenance. The stable INTEGER PRIMARY KEY
-- belongs to the search document, not to events.rowid.
CREATE TRIGGER event_search_documents_after_insert
AFTER INSERT ON event_search_documents
BEGIN
    INSERT INTO event_search_fts(
        rowid, body_text, command_text, input_text, output_text
    ) VALUES (
        NEW.search_document_id,
        NEW.body_text,
        NEW.command_text,
        NEW.input_text,
        NEW.output_text
    );
END;

CREATE TRIGGER event_search_documents_after_delete
AFTER DELETE ON event_search_documents
BEGIN
    INSERT INTO event_search_fts(
        event_search_fts, rowid, body_text, command_text, input_text, output_text
    ) VALUES (
        'delete',
        OLD.search_document_id,
        OLD.body_text,
        OLD.command_text,
        OLD.input_text,
        OLD.output_text
    );
END;

CREATE TRIGGER event_search_documents_after_update
AFTER UPDATE OF body_text, command_text, input_text, output_text
ON event_search_documents
BEGIN
    INSERT INTO event_search_fts(
        event_search_fts, rowid, body_text, command_text, input_text, output_text
    ) VALUES (
        'delete',
        OLD.search_document_id,
        OLD.body_text,
        OLD.command_text,
        OLD.input_text,
        OLD.output_text
    );
    INSERT INTO event_search_fts(
        rowid, body_text, command_text, input_text, output_text
    ) VALUES (
        NEW.search_document_id,
        NEW.body_text,
        NEW.command_text,
        NEW.input_text,
        NEW.output_text
    );
END;

-- New writes are indexed transactionally. INSERT ... DO NOTHING makes this
-- safe when a backfill batch already materialized the same event; the following
-- UPDATE refreshes an existing document from the canonical projection.
CREATE TRIGGER events_search_after_insert
AFTER INSERT ON events
BEGIN
    INSERT INTO event_search_documents(
        event_id, body_text, command_text, input_text, output_text
    )
    SELECT event_id, body_text, command_text, input_text, output_text
      FROM event_search_projection
     WHERE event_id = NEW.id
    ON CONFLICT(event_id) DO NOTHING;
END;

CREATE TRIGGER events_search_after_body_update
AFTER UPDATE OF body, body_availability ON events
BEGIN
    INSERT INTO event_search_documents(
        event_id, body_text, command_text, input_text, output_text
    )
    SELECT event_id, body_text, command_text, input_text, output_text
      FROM event_search_projection
     WHERE event_id = NEW.id
    ON CONFLICT(event_id) DO NOTHING;

    UPDATE event_search_documents
       SET (body_text, command_text, input_text, output_text) = (
           SELECT body_text, command_text, input_text, output_text
             FROM event_search_projection
            WHERE event_id = NEW.id
       )
     WHERE event_id = NEW.id;
END;

CREATE TRIGGER command_audits_search_after_insert
AFTER INSERT ON command_audits
BEGIN
    INSERT INTO event_search_documents(
        event_id, body_text, command_text, input_text, output_text
    )
    SELECT event_id, body_text, command_text, input_text, output_text
      FROM event_search_projection
     WHERE event_id = NEW.event_id
    ON CONFLICT(event_id) DO NOTHING;

    UPDATE event_search_documents
       SET (body_text, command_text, input_text, output_text) = (
           SELECT body_text, command_text, input_text, output_text
             FROM event_search_projection
            WHERE event_id = NEW.event_id
       )
     WHERE event_id = NEW.event_id;
END;

CREATE TRIGGER command_audits_search_after_update
AFTER UPDATE OF command_text, input_text, output_text ON command_audits
BEGIN
    INSERT INTO event_search_documents(
        event_id, body_text, command_text, input_text, output_text
    )
    SELECT event_id, body_text, command_text, input_text, output_text
      FROM event_search_projection
     WHERE event_id = NEW.event_id
    ON CONFLICT(event_id) DO NOTHING;

    UPDATE event_search_documents
       SET (body_text, command_text, input_text, output_text) = (
           SELECT body_text, command_text, input_text, output_text
             FROM event_search_projection
            WHERE event_id = NEW.event_id
       )
     WHERE event_id = NEW.event_id;
END;

CREATE TRIGGER command_audits_search_after_delete
AFTER DELETE ON command_audits
BEGIN
    UPDATE event_search_documents
       SET (body_text, command_text, input_text, output_text) = (
           SELECT body_text, command_text, input_text, output_text
             FROM event_search_projection
            WHERE event_id = OLD.event_id
       )
     WHERE event_id = OLD.event_id;
END;

CREATE TABLE event_search_backfill_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    last_event_id TEXT NOT NULL DEFAULT '',
    target_event_id TEXT,
    completed INTEGER NOT NULL DEFAULT 0 CHECK (completed IN (0, 1)),
    updated_at TEXT NOT NULL
);

INSERT INTO event_search_backfill_state(
    singleton, last_event_id, target_event_id, completed, updated_at
) VALUES (1, '', NULL, 0, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
