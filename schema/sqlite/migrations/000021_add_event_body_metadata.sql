ALTER TABLE events ADD COLUMN body_original_bytes INTEGER
    CHECK (body_original_bytes IS NULL OR body_original_bytes >= 0);
ALTER TABLE events ADD COLUMN body_stored_bytes INTEGER
    CHECK (body_stored_bytes IS NULL OR body_stored_bytes >= 0);
ALTER TABLE events ADD COLUMN body_ingest_truncated INTEGER
    CHECK (body_ingest_truncated IS NULL OR body_ingest_truncated IN (0, 1));
ALTER TABLE events ADD COLUMN body_storage_truncated INTEGER
    CHECK (body_storage_truncated IS NULL OR body_storage_truncated IN (0, 1));
ALTER TABLE events ADD COLUMN body_metadata_version INTEGER
    CHECK (body_metadata_version IS NULL OR body_metadata_version >= 0);

-- Historical rows can prove only their stored byte count. Original size and
-- truncation provenance remain NULL rather than being invented as zero/false.
UPDATE events
   SET body_stored_bytes = length(CAST(body AS BLOB))
 WHERE body_stored_bytes IS NULL;

-- Existing insert paths intentionally remain compatible. The trigger records
-- stored UTF-8 bytes without requiring callers to materialize body content a
-- second time or to claim knowledge of original/truncation facts they do not
-- possess.
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
BEGIN
    UPDATE events
       SET body_stored_bytes = length(CAST(NEW.body AS BLOB))
     WHERE id = NEW.id;
END;
