-- Reader-first payload codec foundation. v0.34 writes identity only; zstd
-- activation and legacy backfill are intentionally deferred to v0.35.
CREATE TABLE store_format_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    minimum_reader_version INTEGER NOT NULL,
    maximum_payload_format INTEGER NOT NULL
);
INSERT INTO store_format_state(singleton, minimum_reader_version, maximum_payload_format)
VALUES (1, 34, 1);

ALTER TABLE events ADD COLUMN body_codec TEXT NOT NULL DEFAULT 'identity';
ALTER TABLE events ADD COLUMN body_format_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE events ADD COLUMN body_plaintext_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_encoded_bytes INTEGER;
ALTER TABLE events ADD COLUMN body_sha256 TEXT;

ALTER TABLE command_audits ADD COLUMN command_codec TEXT NOT NULL DEFAULT 'identity';
ALTER TABLE command_audits ADD COLUMN command_format_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE command_audits ADD COLUMN command_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN command_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN command_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN input_codec TEXT NOT NULL DEFAULT 'identity';
ALTER TABLE command_audits ADD COLUMN input_format_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE command_audits ADD COLUMN input_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN input_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN input_sha256 TEXT;
ALTER TABLE command_audits ADD COLUMN output_codec TEXT NOT NULL DEFAULT 'identity';
ALTER TABLE command_audits ADD COLUMN output_format_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE command_audits ADD COLUMN output_plaintext_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN output_encoded_bytes INTEGER;
ALTER TABLE command_audits ADD COLUMN output_sha256 TEXT;
