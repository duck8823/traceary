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

-- These partial indexes make the global "compressed payload exists" guard
-- proportional to compressed rows rather than total retained history. Search
-- uses the guard to avoid treating a stale plaintext projection as complete.
CREATE INDEX idx_events_nonidentity_body_codec ON events(body_codec)
WHERE body_codec IS NOT NULL AND body_codec <> 'identity';
CREATE INDEX idx_command_audits_nonidentity_command_codec ON command_audits(command_codec)
WHERE command_codec IS NOT NULL AND command_codec <> 'identity';
CREATE INDEX idx_command_audits_nonidentity_input_codec ON command_audits(input_codec)
WHERE input_codec IS NOT NULL AND input_codec <> 'identity';
CREATE INDEX idx_command_audits_nonidentity_output_codec ON command_audits(output_codec)
WHERE output_codec IS NOT NULL AND output_codec <> 'identity';
