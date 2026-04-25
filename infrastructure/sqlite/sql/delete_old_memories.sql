DELETE FROM memories
WHERE status IN ('expired', 'superseded')
  AND updated_at < ?
