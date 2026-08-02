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

-- Constant-size compatibility evidence. ALTER initializes every new codec
-- column to NULL, so all four counters are exactly zero without scanning rows.
CREATE TABLE payload_codec_compatibility_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    mode TEXT NOT NULL CHECK(mode IN('counter','legacy_index')),
    state TEXT NOT NULL CHECK(state IN('valid','invalid')),
    event_body_nonidentity INTEGER NOT NULL CHECK(event_body_nonidentity>=0),
    audit_command_nonidentity INTEGER NOT NULL CHECK(audit_command_nonidentity>=0),
    audit_input_nonidentity INTEGER NOT NULL CHECK(audit_input_nonidentity>=0),
    audit_output_nonidentity INTEGER NOT NULL CHECK(audit_output_nonidentity>=0)
);
INSERT INTO payload_codec_compatibility_state VALUES(1,'counter','valid',0,0,0,0);

CREATE TRIGGER payload_codec_events_insert AFTER INSERT ON events
WHEN new.body_codec IS NOT NULL AND new.body_codec<>'identity'
BEGIN UPDATE payload_codec_compatibility_state SET event_body_nonidentity=event_body_nonidentity+1 WHERE singleton=1 AND mode='counter' AND state='valid'; SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'invalid payload codec compatibility state') END; END;
CREATE TRIGGER payload_codec_events_update_guard BEFORE UPDATE OF body_codec ON events
WHEN old.body_codec IS NOT NULL AND old.body_codec<>'identity'
BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM payload_codec_compatibility_state WHERE singleton=1 AND mode='counter' AND state='valid' AND event_body_nonidentity>0) THEN RAISE(ABORT,'payload codec counter underflow') END; END;
CREATE TRIGGER payload_codec_events_update AFTER UPDATE OF body_codec ON events
WHEN COALESCE(old.body_codec,'identity')<>COALESCE(new.body_codec,'identity')
BEGIN UPDATE payload_codec_compatibility_state SET event_body_nonidentity=event_body_nonidentity-(old.body_codec IS NOT NULL AND old.body_codec<>'identity')+(new.body_codec IS NOT NULL AND new.body_codec<>'identity') WHERE singleton=1 AND mode='counter' AND state='valid'; SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'invalid payload codec compatibility state') END; END;
CREATE TRIGGER payload_codec_events_delete BEFORE DELETE ON events
WHEN old.body_codec IS NOT NULL AND old.body_codec<>'identity'
BEGIN UPDATE payload_codec_compatibility_state SET event_body_nonidentity=event_body_nonidentity-1 WHERE singleton=1 AND mode='counter' AND state='valid' AND event_body_nonidentity>0; SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'payload codec counter underflow') END; END;

CREATE TRIGGER payload_codec_audits_insert AFTER INSERT ON command_audits
WHEN (new.command_codec IS NOT NULL AND new.command_codec<>'identity') OR (new.input_codec IS NOT NULL AND new.input_codec<>'identity') OR (new.output_codec IS NOT NULL AND new.output_codec<>'identity')
BEGIN UPDATE payload_codec_compatibility_state SET audit_command_nonidentity=audit_command_nonidentity+(new.command_codec IS NOT NULL AND new.command_codec<>'identity'),audit_input_nonidentity=audit_input_nonidentity+(new.input_codec IS NOT NULL AND new.input_codec<>'identity'),audit_output_nonidentity=audit_output_nonidentity+(new.output_codec IS NOT NULL AND new.output_codec<>'identity') WHERE singleton=1 AND mode='counter' AND state='valid'; SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'invalid payload codec compatibility state') END; END;
CREATE TRIGGER payload_codec_audits_update_guard BEFORE UPDATE OF command_codec,input_codec,output_codec ON command_audits
BEGIN SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM payload_codec_compatibility_state WHERE singleton=1 AND mode='counter' AND state='valid' AND audit_command_nonidentity >= (old.command_codec IS NOT NULL AND old.command_codec<>'identity') AND audit_input_nonidentity >= (old.input_codec IS NOT NULL AND old.input_codec<>'identity') AND audit_output_nonidentity >= (old.output_codec IS NOT NULL AND old.output_codec<>'identity')) THEN RAISE(ABORT,'payload codec counter underflow') END; END;
CREATE TRIGGER payload_codec_audits_update AFTER UPDATE OF command_codec,input_codec,output_codec ON command_audits
WHEN COALESCE(old.command_codec,'identity')<>COALESCE(new.command_codec,'identity') OR COALESCE(old.input_codec,'identity')<>COALESCE(new.input_codec,'identity') OR COALESCE(old.output_codec,'identity')<>COALESCE(new.output_codec,'identity')
BEGIN UPDATE payload_codec_compatibility_state SET audit_command_nonidentity=audit_command_nonidentity-(old.command_codec IS NOT NULL AND old.command_codec<>'identity')+(new.command_codec IS NOT NULL AND new.command_codec<>'identity'),audit_input_nonidentity=audit_input_nonidentity-(old.input_codec IS NOT NULL AND old.input_codec<>'identity')+(new.input_codec IS NOT NULL AND new.input_codec<>'identity'),audit_output_nonidentity=audit_output_nonidentity-(old.output_codec IS NOT NULL AND old.output_codec<>'identity')+(new.output_codec IS NOT NULL AND new.output_codec<>'identity') WHERE singleton=1 AND mode='counter' AND state='valid'; SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'invalid payload codec compatibility state') END; END;
CREATE TRIGGER payload_codec_audits_delete BEFORE DELETE ON command_audits
WHEN (old.command_codec IS NOT NULL AND old.command_codec<>'identity') OR (old.input_codec IS NOT NULL AND old.input_codec<>'identity') OR (old.output_codec IS NOT NULL AND old.output_codec<>'identity')
BEGIN UPDATE payload_codec_compatibility_state SET audit_command_nonidentity=audit_command_nonidentity-(old.command_codec IS NOT NULL AND old.command_codec<>'identity'),audit_input_nonidentity=audit_input_nonidentity-(old.input_codec IS NOT NULL AND old.input_codec<>'identity'),audit_output_nonidentity=audit_output_nonidentity-(old.output_codec IS NOT NULL AND old.output_codec<>'identity') WHERE singleton=1 AND mode='counter' AND state='valid' AND audit_command_nonidentity >= (old.command_codec IS NOT NULL AND old.command_codec<>'identity') AND audit_input_nonidentity >= (old.input_codec IS NOT NULL AND old.input_codec<>'identity') AND audit_output_nonidentity >= (old.output_codec IS NOT NULL AND old.output_codec<>'identity'); SELECT CASE WHEN changes()<>1 THEN RAISE(ABORT,'payload codec counter underflow') END; END;
