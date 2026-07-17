DELETE FROM memories
WHERE status IN ('expired', 'superseded', 'rejected')
  AND updated_at < ?
