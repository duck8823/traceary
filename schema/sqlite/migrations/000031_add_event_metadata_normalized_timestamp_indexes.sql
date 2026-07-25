-- Persist a lexically sortable RFC3339Nano timestamp for event metadata reads.
--
-- created_at uses Go's variable-width RFC3339Nano representation, which cannot
-- be ordered correctly as TEXT around an exact-second boundary. This migration
-- keeps created_at unchanged, backfills a fixed-width companion column, and
-- maintains it for every later insert or created_at update. The expression is
-- deliberately SQLite built-in only: migrations must remain valid for stock
-- sqlite3 tools that do not register Traceary's ts_norm extension.
--
-- Traceary writes UTC timestamps ending in Z. Historical malformed values keep
-- their legacy lexical shape rather than making migration fail.
ALTER TABLE events ADD COLUMN created_at_norm TEXT;

UPDATE events
   SET created_at_norm = CASE
       WHEN substr(created_at, -1) = 'Z' AND length(created_at) >= 20
       THEN substr(created_at, 1, 19) || '.' ||
            substr(
                CASE
                    WHEN substr(created_at, 20, 1) = '.'
                    THEN substr(created_at, 21, length(created_at) - 21)
                    ELSE ''
                END || '000000000',
                1, 9
            ) || 'Z'
       ELSE created_at
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

CREATE INDEX idx_events_created_at_norm_id_desc
    ON events(created_at_norm DESC, id DESC);
CREATE INDEX idx_events_workspace_created_at_norm_id_desc
    ON events(workspace, created_at_norm DESC, id DESC);
CREATE INDEX idx_events_session_created_at_norm_id_desc
    ON events(session_id, created_at_norm DESC, id DESC);
CREATE INDEX idx_events_workspace_session_created_at_norm_id_desc
    ON events(workspace, session_id, created_at_norm DESC, id DESC);
CREATE INDEX idx_events_source_hook_created_at_norm_id_desc
    ON events(source_hook, created_at_norm DESC, id DESC)
    WHERE source_hook IS NOT NULL;
