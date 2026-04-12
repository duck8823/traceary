SELECT
    id,
    type,
    scope_kind,
    scope_value,
    fact,
    status,
    confidence,
    source,
    supersedes_memory_id,
    expires_at,
    created_at,
    updated_at
FROM memories
WHERE id = ?;
