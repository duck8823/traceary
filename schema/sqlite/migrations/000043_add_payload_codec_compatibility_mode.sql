-- An old development database that already applied the original migration 36
-- has the four partial indexes and no marker. Select legacy mode without
-- scanning canonical payload rows. Rewritten migration 36 creates counter mode
-- first, so CREATE/INSERT OR IGNORE preserves that shape.
CREATE TABLE IF NOT EXISTS payload_codec_compatibility_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 mode TEXT NOT NULL CHECK(mode IN('counter','legacy_index')),
 state TEXT NOT NULL CHECK(state IN('valid','invalid')),
 event_body_nonidentity INTEGER NOT NULL CHECK(event_body_nonidentity>=0),
 audit_command_nonidentity INTEGER NOT NULL CHECK(audit_command_nonidentity>=0),
 audit_input_nonidentity INTEGER NOT NULL CHECK(audit_input_nonidentity>=0),
 audit_output_nonidentity INTEGER NOT NULL CHECK(audit_output_nonidentity>=0)
);
INSERT OR IGNORE INTO payload_codec_compatibility_state VALUES(1,'legacy_index','valid',0,0,0,0);
