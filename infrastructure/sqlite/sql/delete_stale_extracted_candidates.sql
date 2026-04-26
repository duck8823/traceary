DELETE FROM memories
WHERE status = 'candidate'
  AND source IN ('extracted', 'extracted-hidden')
  AND updated_at < ?
