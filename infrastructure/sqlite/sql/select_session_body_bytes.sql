-- Whole-session body-byte pressure (no refinement covers_to yet).
-- Prefer body_plaintext_bytes (codec metadata) so compression does not cut
-- consolidation pressure; fall back to stored length for legacy plaintext rows
-- whose codec columns are all-NULL (#1685 D6).
SELECT COALESCE(SUM(COALESCE(body_plaintext_bytes, length(CAST(body AS BLOB)))), 0)
  FROM events
 WHERE session_id = ?
