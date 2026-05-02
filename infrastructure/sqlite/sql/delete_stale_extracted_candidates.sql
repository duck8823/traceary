DELETE FROM memories
WHERE status = 'candidate'
  AND source IN ('extracted', 'extracted-hidden', 'compact-summary')
  AND updated_at < ?
