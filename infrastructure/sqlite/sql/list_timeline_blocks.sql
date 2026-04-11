-- Gap-based work block detection.
-- When workspace is filtered, LAG operates only on matching rows (correct).
-- When workspace is empty (all workspaces), cross-workspace gaps are treated
-- as continuous work, which is the intended behavior for overview timelines.
WITH ordered_events AS (
  SELECT
    e.id,
    e.kind,
    e.client,
    e.agent,
    e.workspace,
    e.body,
    e.created_at,
    LAG(e.created_at) OVER (ORDER BY e.created_at, e.id) AS prev_created_at
  FROM events e
  WHERE (? = '' OR e.workspace = ?)
    AND (? = '' OR e.created_at >= ?)
    AND (? = '' OR e.created_at < ?)
),
blocks AS (
  SELECT
    *,
    SUM(CASE
      WHEN prev_created_at IS NULL THEN 1
      WHEN (julianday(created_at) - julianday(prev_created_at)) * 86400 > ? THEN 1
      ELSE 0
    END) OVER (ORDER BY created_at, id) AS block_num
  FROM ordered_events
)
SELECT
  block_num,
  MIN(created_at) AS block_start,
  MAX(created_at) AS block_end,
  COUNT(*) AS event_count,
  GROUP_CONCAT(DISTINCT workspace) AS workspaces,
  GROUP_CONCAT(DISTINCT agent) AS agents,
  GROUP_CONCAT(kind, '|') AS kinds
FROM blocks
GROUP BY block_num
ORDER BY block_start DESC
LIMIT ?
