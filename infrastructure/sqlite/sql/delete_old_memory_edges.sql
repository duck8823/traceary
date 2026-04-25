DELETE FROM memory_edges
WHERE (valid_to IS NOT NULL AND valid_to < ?)
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
