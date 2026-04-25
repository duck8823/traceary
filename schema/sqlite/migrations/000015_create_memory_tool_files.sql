CREATE TABLE IF NOT EXISTS memory_tool_files (
    path TEXT PRIMARY KEY CHECK (path LIKE '/memories/%'),
    content BLOB NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0)
);

