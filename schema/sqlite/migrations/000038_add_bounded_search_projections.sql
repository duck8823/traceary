-- Additive, rebuildable projection. Legacy search remains authoritative.
CREATE TABLE search_projection_source_revision(singleton INTEGER PRIMARY KEY CHECK(singleton=1), revision INTEGER NOT NULL);
INSERT INTO search_projection_source_revision VALUES(1,0);
CREATE TRIGGER search_projection_events_update AFTER UPDATE OF body,body_availability,session_id,kind,created_at ON events BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;
CREATE TRIGGER search_projection_events_delete AFTER DELETE ON events BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;
CREATE TRIGGER search_projection_audits_insert AFTER INSERT ON command_audits BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;
CREATE TRIGGER search_projection_audits_update AFTER UPDATE ON command_audits BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;
CREATE TRIGGER search_projection_audits_delete AFTER DELETE ON command_audits BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;

CREATE TABLE search_projection_recent_documents(generation_id TEXT NOT NULL,event_rowid INTEGER NOT NULL,event_id TEXT NOT NULL,created_at_norm TEXT NOT NULL,body_text TEXT NOT NULL,decoded_bytes INTEGER NOT NULL,PRIMARY KEY(generation_id,event_rowid));
CREATE INDEX idx_search_projection_recent_eviction ON search_projection_recent_documents(generation_id,created_at_norm,event_rowid);
CREATE VIRTUAL TABLE search_projection_recent_fts USING fts5(body_text,content='search_projection_recent_documents',content_rowid='rowid',tokenize='trigram case_sensitive 1');
CREATE TRIGGER search_projection_recent_ai AFTER INSERT ON search_projection_recent_documents BEGIN INSERT INTO search_projection_recent_fts(rowid,body_text) VALUES(new.rowid,new.body_text); END;
CREATE TRIGGER search_projection_recent_ad AFTER DELETE ON search_projection_recent_documents BEGIN INSERT INTO search_projection_recent_fts(search_projection_recent_fts,rowid,body_text) VALUES('delete',old.rowid,old.body_text); END;

CREATE TABLE search_projection_session_summaries(generation_id TEXT NOT NULL,session_id TEXT NOT NULL,event_count INTEGER NOT NULL,summary_text TEXT NOT NULL,projection_version INTEGER NOT NULL,PRIMARY KEY(generation_id,session_id));
CREATE TABLE search_projection_command_aggregates(generation_id TEXT NOT NULL,session_id TEXT NOT NULL,command_count INTEGER NOT NULL,failure_count INTEGER NOT NULL,PRIMARY KEY(generation_id,session_id));
CREATE TABLE search_projection_session_keywords(generation_id TEXT NOT NULL,session_id TEXT NOT NULL,keyword TEXT NOT NULL,occurrences INTEGER NOT NULL,keyword_version INTEGER NOT NULL,PRIMARY KEY(generation_id,session_id,keyword));
CREATE TABLE search_projection_state(singleton INTEGER PRIMARY KEY CHECK(singleton=1),generation_id TEXT,config_hash TEXT NOT NULL DEFAULT '',source_revision INTEGER NOT NULL DEFAULT 0,high_water INTEGER NOT NULL DEFAULT 0,checkpoint INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'idle' CHECK(state IN('idle','rebuilding','complete','drifted')),projection_version INTEGER NOT NULL DEFAULT 1,fts_design TEXT NOT NULL DEFAULT 'external_content',recent_age_seconds INTEGER NOT NULL DEFAULT 0,recent_byte_limit INTEGER NOT NULL DEFAULT 0,last_batch_milliseconds INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL);
INSERT INTO search_projection_state(singleton,updated_at) VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
