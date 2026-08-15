SELECT COUNT(*)
FROM memories
WHERE status IN ('expired', 'superseded', 'rejected')
  AND ts_norm(updated_at) < ts_norm(?)
