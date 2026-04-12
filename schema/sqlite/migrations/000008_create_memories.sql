CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope_kind TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    fact TEXT NOT NULL,
    status TEXT NOT NULL,
    confidence TEXT NOT NULL,
    source TEXT NOT NULL,
    supersedes_memory_id TEXT REFERENCES memories(id),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_memories_scope_status_updated
    ON memories(scope_kind, scope_value, status, updated_at DESC, id DESC);

CREATE INDEX idx_memories_type_status_updated
    ON memories(type, status, updated_at DESC, id DESC);

CREATE INDEX idx_memories_supersedes_memory_id
    ON memories(supersedes_memory_id);

CREATE TABLE memory_evidence_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);

CREATE INDEX idx_memory_evidence_refs_lookup
    ON memory_evidence_refs(ref_kind, ref_value);

CREATE TABLE memory_artifact_refs (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    ref_kind TEXT NOT NULL,
    ref_value TEXT NOT NULL,
    PRIMARY KEY (memory_id, ordinal)
);

CREATE INDEX idx_memory_artifact_refs_lookup
    ON memory_artifact_refs(ref_kind, ref_value);
