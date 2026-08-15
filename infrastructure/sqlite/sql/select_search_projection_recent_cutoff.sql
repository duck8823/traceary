-- Source-phase prefilter: walk the newest N rows (first bind: LIMIT) and
-- find the first created_at_norm at which the running sum of *prefilter*
-- source bytes exceeds the walk ceiling (second bind). Caller applies slack
-- — see deriveSearchProjectionRecentCutoff. N is a work bound so a large
-- store still gets an answer (#1807). Eviction enforces the exact ceiling.
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
-- No row means the sampled window fits under the walk ceiling; the caller
-- then distinguishes "whole corpus fits" from "sample exhausted".
SELECT created_at_norm FROM (
  SELECT created_at_norm,
         SUM(CASE WHEN body_availability = 'available'
                  THEN COALESCE(body_plaintext_bytes, body_stored_bytes, length(CAST(body AS BLOB)), 0)
                  ELSE 0 END
             + COALESCE(command_plaintext_bytes, length(CAST(command_text AS BLOB)), 0)
             + COALESCE(input_plaintext_bytes, length(CAST(input_text AS BLOB)), 0)
             + COALESCE(output_plaintext_bytes, length(CAST(output_text AS BLOB)), 0))
           OVER (ORDER BY created_at_norm DESC, id DESC) AS running
    FROM (
      SELECT e.created_at_norm AS created_at_norm,
             e.id AS id,
             e.body_availability AS body_availability,
             e.body_plaintext_bytes AS body_plaintext_bytes,
             e.body_stored_bytes AS body_stored_bytes,
             e.body AS body,
             a.command_plaintext_bytes AS command_plaintext_bytes,
             a.command_text AS command_text,
             a.input_plaintext_bytes AS input_plaintext_bytes,
             a.input_text AS input_text,
             a.output_plaintext_bytes AS output_plaintext_bytes,
             a.output_text AS output_text
        FROM events e
        LEFT JOIN command_audits a ON a.event_id = e.id
       ORDER BY e.created_at_norm DESC, e.id DESC
       LIMIT ?
    ) newest
) ranked
 WHERE running > ?
 ORDER BY created_at_norm DESC
 LIMIT 1
