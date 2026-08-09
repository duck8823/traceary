-- Walk the corpus newest-first and find the first created_at_norm at which
-- the running sum of indexable source bytes exceeds the source ceiling.
-- The byte expression matches SelectSnapshot's DecodedBytes accounting
-- (length(CAST(x AS BLOB)) counts bytes, not characters — #1749).
-- No row means the whole corpus fits; the caller leaves the cutoff empty.
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
