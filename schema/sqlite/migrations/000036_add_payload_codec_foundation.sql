-- Reader-first payload codec foundation. v0.34 writes identity only; zstd
-- activation and legacy backfill are intentionally deferred to v0.35.
CREATE TABLE store_format_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    minimum_reader_version INTEGER NOT NULL,
    maximum_payload_format INTEGER NOT NULL
);
INSERT INTO store_format_state(singleton, minimum_reader_version, maximum_payload_format)
VALUES (1, 34, 1);

ALTER TABLE events ADD COLUMN body_codec TEXT;
ALTER TABLE events ADD COLUMN body_format_version INTEGER;
ALTER TABLE events ADD COLUMN body_plaintext_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_encoded_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_sha256 TEXT;

ALTER TABLE command_audits ADD COLUMN command_codec TEXT;
ALTER TABLE command_audits ADD COLUMN command_format_version INTEGER;
ALTER TABLE command_audits ADD COLUMN command_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN command_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN command_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN input_codec TEXT;
ALTER TABLE command_audits ADD COLUMN input_format_version INTEGER;
ALTER TABLE command_audits ADD COLUMN input_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN input_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN input_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN output_codec TEXT;
ALTER TABLE command_audits ADD COLUMN output_format_version INTEGER;
ALTER TABLE command_audits ADD COLUMN output_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN output_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN output_sha256 TEXT;

-- Compatibility for tools that directly edit legacy identity payload text.
-- Codec-aware writers update body and metadata atomically, so the checksum
-- equality guard prevents this trigger from overriding their values.
CREATE TRIGGER events_identity_payload_metadata_after_update
AFTER UPDATE OF body ON events
WHEN OLD.body_codec = 'identity' AND NEW.body_codec = 'identity'
 AND NEW.body_sha256 = OLD.body_sha256
BEGIN
  UPDATE events SET
    body_plaintext_bytes = length(CAST(NEW.body AS BLOB)),
    body_encoded_bytes = length(CAST(NEW.body AS BLOB)),
    body_sha256 = traceary_sha256(NEW.body)
  WHERE id = NEW.id;
END;
