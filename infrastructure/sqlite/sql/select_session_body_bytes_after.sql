-- Body-byte pressure strictly after a covers_to boundary under canonical order
-- (ts_norm(created_at), id). Plain TEXT comparison of variable-width
-- RFC3339Nano is wrong (#1185): '.' < 'Z' would invert fractional vs whole
-- second timestamps.
-- Prefer body_plaintext_bytes so compression does not cut consolidation
-- pressure; fall back to stored length for legacy plaintext rows (#1685 D6).
SELECT COALESCE(SUM(COALESCE(e.body_plaintext_bytes, length(CAST(e.body AS BLOB)))), 0)
  FROM events AS e
  JOIN events AS boundary ON boundary.id = ?
 WHERE e.session_id = ?
   AND (
         ts_norm(e.created_at) > ts_norm(boundary.created_at)
      OR (
             ts_norm(e.created_at) = ts_norm(boundary.created_at)
         AND e.id > boundary.id
         )
       )
