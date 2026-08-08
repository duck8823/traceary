-- Eligible finished session summaries for wake injection (#1684).
-- Top-level sessions only; non-degraded refinements; newest first.
-- Callers apply the byte budget in Go and never truncate mid-summary.
-- started_at is variable-width RFC3339 (24-27 chars on the live store), so raw
-- lexical DESC misorders whole-second against sub-second timestamps ('.' 0x2E
-- sorts below 'Z' 0x5A, #1185). ts_norm fixes the width; the workspace equality
-- still uses idx_sessions_repo_started_at, only the ordering needs a sort.
SELECT
  s.session_id,
  r.summary
FROM sessions s
INNER JOIN session_refinements r ON r.session_id = s.session_id
WHERE s.workspace = ?
  AND (s.parent_session_id IS NULL OR s.parent_session_id = '')
  AND s.session_id != ?
  AND r.degraded = 0
ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
LIMIT ?
