UPDATE memories
   SET supersedes_memory_id = NULL
 WHERE supersedes_memory_id IN (
       SELECT id
         FROM memories
        WHERE status IN ('expired', 'superseded', 'rejected')
          AND updated_at < ?
 )
