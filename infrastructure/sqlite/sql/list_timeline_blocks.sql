-- Gap-based work block detection with per-workspace breakdown.
--
-- The CTE chain:
--   ordered_events : filter + LAG over created_at
--   blocks         : assign block_num using gap threshold
--   block_summary  : one row per block with aggregates
--   top_blocks    : block_summary ordered DESC + LIMIT N
--   first_prompt   : first prompt body per (block, workspace)
--   last_compact   : last compact_summary body per (block, workspace)
--   ws_rows        : one row per (block, workspace) with counts / kinds /
--                    joined summary candidates
--
-- The final SELECT returns one row per (block, workspace) for the top N
-- blocks; Go assembles per-block breakdown by grouping on block_num.
--
-- When workspace is filtered, LAG operates only on matching rows (correct).
-- When workspace is empty (all workspaces), cross-workspace gaps are treated
-- as continuous work, which is the intended behavior for overview timelines.
WITH ordered_events AS (
  SELECT
    e.id,
    e.kind,
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
),
block_summary AS (
  SELECT
    block_num,
    MIN(created_at) AS block_start,
    MAX(created_at) AS block_end,
    COUNT(*) AS block_event_count,
    GROUP_CONCAT(DISTINCT agent) AS agents
  FROM blocks
  GROUP BY block_num
),
top_blocks AS (
  SELECT *
  FROM block_summary
  ORDER BY block_start DESC
  LIMIT ?
),
prompt_ranked AS (
  SELECT
    block_num,
    workspace,
    body,
    ROW_NUMBER() OVER (PARTITION BY block_num, workspace ORDER BY created_at, id) AS rn
  FROM blocks
  WHERE kind = 'prompt'
    AND TRIM(body) != ''
),
first_prompt AS (
  SELECT block_num, workspace, body AS first_prompt_body
  FROM prompt_ranked
  WHERE rn = 1
),
compact_ranked AS (
  SELECT
    block_num,
    workspace,
    body,
    ROW_NUMBER() OVER (PARTITION BY block_num, workspace ORDER BY created_at DESC, id DESC) AS rn
  FROM blocks
  WHERE kind = 'compact_summary'
    AND TRIM(body) != ''
),
last_compact AS (
  SELECT block_num, workspace, body AS compact_summary_body
  FROM compact_ranked
  WHERE rn = 1
),
ws_rows AS (
  SELECT
    b.block_num,
    b.workspace,
    COUNT(*) AS ws_event_count,
    GROUP_CONCAT(b.kind, '|') AS kinds,
    MAX(fp.first_prompt_body) AS first_prompt_body,
    MAX(lc.compact_summary_body) AS compact_summary_body
  FROM blocks b
  LEFT JOIN first_prompt fp
    ON fp.block_num = b.block_num AND fp.workspace = b.workspace
  LEFT JOIN last_compact lc
    ON lc.block_num = b.block_num AND lc.workspace = b.workspace
  WHERE b.block_num IN (SELECT block_num FROM top_blocks)
    AND b.workspace != ''
  GROUP BY b.block_num, b.workspace
)
SELECT
  tb.block_num,
  tb.block_start,
  tb.block_end,
  tb.block_event_count,
  COALESCE(tb.agents, '') AS agents,
  wr.workspace,
  wr.ws_event_count,
  wr.kinds,
  COALESCE(wr.first_prompt_body, '') AS first_prompt_body,
  COALESCE(wr.compact_summary_body, '') AS compact_summary_body
FROM top_blocks tb
JOIN ws_rows wr ON wr.block_num = tb.block_num
ORDER BY tb.block_start DESC, wr.ws_event_count DESC, wr.workspace
