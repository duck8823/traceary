SELECT COUNT(*)
FROM memory_edges
WHERE (
       (valid_to IS NOT NULL AND ts_norm(valid_to) < ts_norm(?))
    OR NOT EXISTS (
        SELECT 1
          FROM memories
         WHERE memories.id = memory_edges.from_memory_id
    )
    OR NOT EXISTS (
        SELECT 1
          FROM memories
         WHERE memories.id = memory_edges.to_memory_id
    )
)
AND NOT EXISTS (
    SELECT 1
      FROM memories
     WHERE memories.id = memory_edges.from_memory_id
       AND memories.status IN ('expired', 'superseded', 'rejected')
       AND ts_norm(memories.updated_at) < ts_norm(?)
)
AND NOT EXISTS (
    SELECT 1
      FROM memories
     WHERE memories.id = memory_edges.to_memory_id
       AND memories.status IN ('expired', 'superseded', 'rejected')
       AND ts_norm(memories.updated_at) < ts_norm(?)
)
