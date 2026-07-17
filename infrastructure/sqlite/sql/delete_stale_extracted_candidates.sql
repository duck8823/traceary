-- Non-destructive decay: transition unreviewed auto-extracted candidates to
-- expired so they remain restorable until keep-days physical GC.
UPDATE memories
SET status = 'expired',
    expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE status = 'candidate'
  AND source IN ('extracted', 'extracted-hidden', 'compact-summary')
  AND updated_at < ?
