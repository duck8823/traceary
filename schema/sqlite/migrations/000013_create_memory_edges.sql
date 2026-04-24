-- Create a minimum-viable temporal-edge overlay on top of the
-- existing memories table so callers can record typed relationships
-- ("supports", "contradicts", "supersedes", "related-to", "causes")
-- between memories without promoting graph primary storage. Each
-- edge carries its own `[valid_from, valid_to)` window, matching
-- the half-open semantics the memory table already uses. See #573.
CREATE TABLE memory_edges (
    id              TEXT PRIMARY KEY,
    from_memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    to_memory_id    TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL,
    valid_from      TEXT NOT NULL,
    valid_to        TEXT,
    created_at      TEXT NOT NULL
);

-- "edges out of X as of T" index.
CREATE INDEX idx_memory_edges_from
    ON memory_edges(from_memory_id, valid_from DESC);

-- Reverse-direction traversal (used by "what depends on Y" lookups).
CREATE INDEX idx_memory_edges_to
    ON memory_edges(to_memory_id, valid_from DESC);
