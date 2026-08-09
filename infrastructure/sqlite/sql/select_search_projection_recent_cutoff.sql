-- Source-phase prefilter: walk the corpus newest-first and find the first
-- created_at_norm at which the running sum of *prefilter* source bytes exceeds
-- the walk ceiling (caller applies a slack factor — see deriveSearchProjectionRecentCutoff).
--
-- This is a build-cost bound, not an enforcement mechanism. Eviction enforces
-- the exact recent_source_ceiling_bytes. The prefilter's byte unit differs from
-- decoded_bytes in both directions:
--   - body_plaintext_bytes counts the whole stored envelope (thinking blocks
--     included); ExtractPlainBody keeps only text blocks, so thinking-only
--     envelopes project to empty body_text / zero decoded_bytes.
--   - command_audits payloads that hydrate into the projected body are counted
--     here, but the reverse is also imperfect — the prefilter is deliberately
--     loose, not exact. Decoding every body is what this walk exists to avoid.
--
-- The byte expression reuses SelectSnapshot-style COALESCE + CAST-as-BLOB
-- accounting (length() on TEXT counts characters, not bytes — #1749).
-- No row means the whole corpus fits under the walk ceiling; the caller leaves
-- the cutoff empty.
SELECT created_at_norm FROM (
  SELECT e.created_at_norm AS created_at_norm,
         SUM(CASE WHEN e.body_availability = 'available'
                  THEN COALESCE(e.body_plaintext_bytes, e.body_stored_bytes, length(CAST(e.body AS BLOB)), 0)
                  ELSE 0 END
             + COALESCE(a.command_plaintext_bytes, length(CAST(a.command_text AS BLOB)), 0)
             + COALESCE(a.input_plaintext_bytes, length(CAST(a.input_text AS BLOB)), 0)
             + COALESCE(a.output_plaintext_bytes, length(CAST(a.output_text AS BLOB)), 0))
           OVER (ORDER BY e.created_at_norm DESC, e.id DESC) AS running
    FROM events e
    LEFT JOIN command_audits a ON a.event_id = e.id
   ORDER BY e.created_at_norm DESC, e.id DESC
) WHERE running > ?
ORDER BY created_at_norm DESC
LIMIT 1
