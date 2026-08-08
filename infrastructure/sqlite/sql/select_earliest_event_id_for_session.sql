-- Canonical order is (ts_norm(created_at), id). created_at is variable-width
-- RFC3339Nano and is not lexically ordered (#1185).
SELECT id
  FROM events
 WHERE session_id = ?
 ORDER BY ts_norm(created_at) ASC, id ASC
 LIMIT 1
