INSERT INTO memories (
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    type = excluded.type,
    scope_kind = excluded.scope_kind,
    scope_value = excluded.scope_value,
    fact = excluded.fact,
    status = excluded.status,
    confidence = excluded.confidence,
    source = excluded.source,
    supersedes_memory_id = excluded.supersedes_memory_id,
    expires_at = excluded.expires_at,
    created_at = excluded.created_at,
    updated_at = excluded.updated_at;
