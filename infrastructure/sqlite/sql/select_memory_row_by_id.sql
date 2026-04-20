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
    valid_from,
    valid_to,
    created_at,
    updated_at
FROM memories
WHERE id = ?;
