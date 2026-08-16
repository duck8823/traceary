-- #1744: the dedupe quarantine archive stores decoded plaintext, which
-- re-inflates the store when a compressed row is quarantined and makes
-- ReleasedBody report logical rather than physical bytes.
--
-- Additive only, mirroring how migration 000036 added the same five columns
-- to `events`: no existing archive row is rewritten. A row with NULL
-- body_codec is a legacy (pre-#1685 or pre-migration) plaintext row and reads
-- as codec=identity, stored_size=length(body) -- the same contract
-- payloadRow.decode already applies to `events`.
ALTER TABLE event_content_dedupe_archive ADD COLUMN body_codec TEXT;
ALTER TABLE event_content_dedupe_archive ADD COLUMN body_format_version INTEGER;
ALTER TABLE event_content_dedupe_archive ADD COLUMN body_plaintext_bytes INTEGER;
ALTER TABLE event_content_dedupe_archive ADD COLUMN body_encoded_bytes INTEGER;
ALTER TABLE event_content_dedupe_archive ADD COLUMN body_sha256 TEXT;
