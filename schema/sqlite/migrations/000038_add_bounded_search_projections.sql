-- Derived, non-authoritative search projections. Legacy event search remains
-- unchanged. External-content FTS is selected deliberately for portable
-- update/delete semantics and attributable content bytes.
CREATE TABLE search_projection_recent_documents (
    document_id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE REFERENCES events(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    body_text TEXT NOT NULL,
    decoded_bytes INTEGER NOT NULL CHECK(decoded_bytes >= 0)
);
CREATE INDEX idx_search_projection_recent_documents_eviction
    ON search_projection_recent_documents(created_at, event_id);
CREATE VIRTUAL TABLE search_projection_recent_fts USING fts5(
    body_text, content='search_projection_recent_documents', content_rowid='document_id',
    tokenize='trigram case_sensitive 1'
);
CREATE TRIGGER search_projection_recent_documents_ai AFTER INSERT ON search_projection_recent_documents BEGIN
  INSERT INTO search_projection_recent_fts(rowid, body_text) VALUES(new.document_id, new.body_text);
END;
CREATE TRIGGER search_projection_recent_documents_ad AFTER DELETE ON search_projection_recent_documents BEGIN
  INSERT INTO search_projection_recent_fts(search_projection_recent_fts,rowid,body_text) VALUES('delete',old.document_id,old.body_text);
END;
CREATE TRIGGER search_projection_recent_documents_au AFTER UPDATE OF body_text ON search_projection_recent_documents BEGIN
  INSERT INTO search_projection_recent_fts(search_projection_recent_fts,rowid,body_text) VALUES('delete',old.document_id,old.body_text);
  INSERT INTO search_projection_recent_fts(rowid,body_text) VALUES(new.document_id,new.body_text);
END;

CREATE TABLE search_projection_session_summaries (
    session_id TEXT PRIMARY KEY,
    event_count INTEGER NOT NULL,
    command_count INTEGER NOT NULL,
    failure_count INTEGER NOT NULL,
    summary_text TEXT NOT NULL,
    projection_version INTEGER NOT NULL
);
CREATE TABLE search_projection_session_keywords (
    session_id TEXT NOT NULL,
    keyword TEXT NOT NULL,
    occurrences INTEGER NOT NULL,
    PRIMARY KEY(session_id, keyword)
);
CREATE TABLE search_projection_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    projection_version INTEGER NOT NULL,
    fts_design TEXT NOT NULL CHECK(fts_design='external_content'),
    last_event_id TEXT NOT NULL DEFAULT '',
    target_event_id TEXT,
    completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN(0,1)),
    recent_age_seconds INTEGER NOT NULL,
    recent_byte_limit INTEGER NOT NULL,
    recent_bytes INTEGER NOT NULL DEFAULT 0,
    recent_documents INTEGER NOT NULL DEFAULT 0,
    summary_sessions INTEGER NOT NULL DEFAULT 0,
    keyword_rows INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);
INSERT INTO search_projection_state(singleton,projection_version,fts_design,recent_age_seconds,recent_byte_limit,updated_at)
VALUES(1,1,'external_content',2592000,67108864,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
