SELECT COUNT(*)
FROM memories
WHERE status = 'candidate'
  AND source IN ('extracted', 'extracted-hidden', 'compact-summary')
  AND ts_norm(updated_at) < ts_norm(?)
