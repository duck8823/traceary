-- A continuation cursor is valid only while durable-memory state is unchanged.
-- The singleton revision is incremented inside the same transaction as every
-- memory insert/update/delete; the scanner compares it before reading a page.
CREATE TABLE memory_hygiene_revision (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    revision INTEGER NOT NULL CHECK (revision >= 0)
);

INSERT INTO memory_hygiene_revision(singleton, revision) VALUES (1, 0);

CREATE TRIGGER memories_hygiene_revision_after_insert
AFTER INSERT ON memories
BEGIN
    UPDATE memory_hygiene_revision
    SET revision = revision + 1
    WHERE singleton = 1;
END;

CREATE TRIGGER memories_hygiene_revision_after_update
AFTER UPDATE ON memories
BEGIN
    UPDATE memory_hygiene_revision
    SET revision = revision + 1
    WHERE singleton = 1;
END;

CREATE TRIGGER memories_hygiene_revision_after_delete
AFTER DELETE ON memories
BEGIN
    UPDATE memory_hygiene_revision
    SET revision = revision + 1
    WHERE singleton = 1;
END;

-- Keyset traversal first orders all rows by status/id, then finds same-scope
-- partners by id. Exact duplicate lookup additionally constrains fact.
CREATE INDEX idx_memories_hygiene_status_id
    ON memories(status, id);

CREATE INDEX idx_memories_hygiene_scope_id
    ON memories(status, scope_kind, scope_value, id);

CREATE INDEX idx_memories_hygiene_exact
    ON memories(status, scope_kind, scope_value, fact, id);

CREATE INDEX idx_memories_hygiene_candidate_source_id
    ON memories(status, source, id);
