UPDATE memories
   SET supersedes_memory_id = NULL
 WHERE supersedes_memory_id IN (
       SELECT id
         FROM memories
        WHERE status = 'candidate'
          AND source IN ('extracted', 'extracted-hidden', 'compact-summary')
          AND updated_at < ?
 )
