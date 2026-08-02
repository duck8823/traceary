-- Additive, deterministically rebuildable historical literal candidates.
-- Absence of a row is unknown, never proof that an event cannot match.
CREATE TABLE literal_search_projection_state(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 generation_id TEXT NOT NULL DEFAULT '',
 high_water INTEGER NOT NULL DEFAULT 0,
 query_revision INTEGER NOT NULL DEFAULT 0,
 fingerprint_version INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'missing' CHECK(state IN('missing','rebuilding','complete','stale')),
 updated_at TEXT NOT NULL
	,cursor_key BLOB NOT NULL DEFAULT (randomblob(32)) CHECK(length(cursor_key)=32)
);
INSERT INTO literal_search_projection_state(singleton,updated_at)
VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));

CREATE TABLE literal_search_fingerprints(
 generation_id TEXT NOT NULL,
 source_sequence INTEGER NOT NULL,
 event_id TEXT NOT NULL,
 fingerprint BLOB NOT NULL CHECK(length(fingerprint)=16),
 fingerprint_version INTEGER NOT NULL CHECK(fingerprint_version=1),
 PRIMARY KEY(generation_id,event_id,fingerprint),
 FOREIGN KEY(source_sequence) REFERENCES search_projection_source_sequence(sequence) ON DELETE CASCADE
);
CREATE INDEX idx_literal_search_fingerprint_candidate
ON literal_search_fingerprints(generation_id,fingerprint,source_sequence,event_id);

-- Writers only invalidate the projection in constant work. A bounded rebuild
-- later creates sorted/deduplicated hashes from codec-decoded canonical text.
CREATE TRIGGER literal_search_event_insert AFTER INSERT ON events BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
CREATE TRIGGER literal_search_event_update AFTER UPDATE OF workspace,client,agent,session_id,kind,created_at,body,body_availability ON events BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
CREATE TRIGGER literal_search_event_delete AFTER DELETE ON events BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
CREATE TRIGGER literal_search_audit_insert AFTER INSERT ON command_audits BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
CREATE TRIGGER literal_search_audit_update AFTER UPDATE ON command_audits BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
CREATE TRIGGER literal_search_audit_delete AFTER DELETE ON command_audits BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;
