-- Stamp scheduled candidate TTL on unreviewed auto-extracted rows that
-- never received expires_at. Modifier is '+N seconds' from created_at.
-- Does not change status; decay expire is a separate pass.
UPDATE memories
   SET expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', substr(created_at, 1, 19), ?)
 WHERE status = 'candidate'
   AND source IN ('extracted', 'extracted-hidden')
   AND expires_at IS NULL
