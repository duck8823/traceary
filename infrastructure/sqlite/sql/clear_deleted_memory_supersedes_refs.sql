UPDATE memories
   SET supersedes_memory_id = NULL
 WHERE supersedes_memory_id IN (
       SELECT id
         FROM memories
        WHERE status IN ('expired', 'superseded', 'rejected')
          AND ts_norm(updated_at) < ts_norm(?)
 )
