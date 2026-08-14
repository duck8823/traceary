-- Attestation chain over new prompt bodies and command identity (#1677).
-- CREATE + genesis head only. No scan of events / command_audits: historical
-- rows stay unattested. output_text is not part of this schema.
CREATE TABLE attestation_links (
    seq INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('prompt', 'command')),
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    prev_sha256 TEXT NOT NULL CHECK (length(prev_sha256) = 64),
    link_sha256 TEXT NOT NULL CHECK (length(link_sha256) = 64),
    created_at TEXT NOT NULL,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE RESTRICT
);

CREATE TABLE attestation_head (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    head_sha256 TEXT NOT NULL CHECK (length(head_sha256) = 64),
    seq INTEGER NOT NULL CHECK (seq >= 0)
);

-- SHA256("traceary.attest.genesis.v1\n") — empty-store predecessor.
INSERT INTO attestation_head (singleton, head_sha256, seq)
VALUES (1, '06f9f940c84ec663a28e6e00555b0b39a9dc289484012dc7d8e9043d7d09f652', 0);
