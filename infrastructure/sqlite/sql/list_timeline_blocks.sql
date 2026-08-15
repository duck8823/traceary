-- Gap-based work block detection with per-workspace breakdown.
--
-- The CTE chain:
--   ordered_events : filter + LAG over created_at
--   blocks         : assign block_num using gap threshold
--   block_summary  : one row per block with aggregates
--   top_blocks    : block_summary ordered DESC + LIMIT N
--   first_prompt   : leading prompt candidate ids per (block, workspace)
--   last_compact   : trailing compact_summary candidate ids per (block, workspace)
--   ws_rows        : one row per (block, workspace) with counts / kinds /
--                    summary candidate ids
--
-- Summary candidates are returned as ranked id lists, never as bodies: an
-- encoded body is a BLOB this query cannot read, so Go decodes in rank order
-- and keeps the first non-blank one (#1685 D6, #1746). TRIM(body) != '' still
-- drops identity whitespace for free. Encoded blanks and tab/newline
-- plaintext survive TRIM, so the list is unbounded — a depth cap would fall
-- through to the next kind instead of the kind's own later candidate.
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
    ts_norm(e.created_at) AS created_at_norm,
    LAG(e.created_at) OVER (ORDER BY ts_norm(e.created_at), e.id) AS prev_created_at
  FROM events e
  WHERE (? = '' OR e.workspace = ?)
    AND (? = '' OR ts_norm(e.created_at) >= ts_norm(?))
    AND (? = '' OR ts_norm(e.created_at) < ts_norm(?))
),
blocks AS (
  SELECT
    *,
    SUM(CASE
      WHEN prev_created_at IS NULL THEN 1
      WHEN (julianday(created_at) - julianday(prev_created_at)) * 86400 > ? THEN 1
      ELSE 0
    END) OVER (ORDER BY created_at_norm, id) AS block_num
  FROM ordered_events
),
block_summary AS (
  SELECT
    block_num,
    MIN(created_at_norm) AS block_start,
    MAX(created_at_norm) AS block_end,
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
    id,
    ROW_NUMBER() OVER (PARTITION BY block_num, workspace ORDER BY created_at_norm, id) AS rn
  FROM blocks
  WHERE kind = 'prompt'
    AND TRIM(body) != ''
),
first_prompt AS (
  SELECT
    block_num,
    workspace,
    json_group_array(id) AS first_prompt_ids
  FROM (
    SELECT block_num, workspace, id
    FROM prompt_ranked
    ORDER BY block_num, workspace, rn
  )
  GROUP BY block_num, workspace
),
compact_ranked AS (
  SELECT
    block_num,
    workspace,
    id,
    ROW_NUMBER() OVER (PARTITION BY block_num, workspace ORDER BY created_at_norm DESC, id DESC) AS rn
  FROM blocks
  WHERE kind = 'compact_summary'
    AND TRIM(body) != ''
),
last_compact AS (
  SELECT
    block_num,
    workspace,
    json_group_array(id) AS compact_summary_ids
  FROM (
    SELECT block_num, workspace, id
    FROM compact_ranked
    ORDER BY block_num, workspace, rn
  )
  GROUP BY block_num, workspace
),
transcript_ranked AS (
  SELECT
    block_num,
    workspace,
    id,
    ROW_NUMBER() OVER (PARTITION BY block_num, workspace ORDER BY created_at_norm, id) AS rn
  FROM blocks
  WHERE kind = 'transcript'
    AND TRIM(body) != ''
),
first_transcript AS (
  SELECT
    block_num,
    workspace,
    json_group_array(id) AS first_transcript_ids
  FROM (
    SELECT block_num, workspace, id
    FROM transcript_ranked
    ORDER BY block_num, workspace, rn
  )
  GROUP BY block_num, workspace
),
ws_rows AS (
  SELECT
    b.block_num,
    b.workspace,
    COUNT(*) AS ws_event_count,
    GROUP_CONCAT(b.kind, '|') AS kinds,
    GROUP_CONCAT(DISTINCT b.agent) AS ws_agents,
    MAX(fp.first_prompt_ids) AS first_prompt_ids,
    MAX(lc.compact_summary_ids) AS compact_summary_ids,
    MAX(ft.first_transcript_ids) AS first_transcript_ids
  FROM blocks b
  LEFT JOIN first_prompt fp
    ON fp.block_num = b.block_num AND fp.workspace = b.workspace
  LEFT JOIN last_compact lc
    ON lc.block_num = b.block_num AND lc.workspace = b.workspace
  LEFT JOIN first_transcript ft
    ON ft.block_num = b.block_num AND ft.workspace = b.workspace
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
  COALESCE(wr.ws_agents, '') AS ws_agents,
  COALESCE(wr.first_prompt_ids, '[]') AS first_prompt_ids,
  COALESCE(wr.compact_summary_ids, '[]') AS compact_summary_ids,
  COALESCE(wr.first_transcript_ids, '[]') AS first_transcript_ids
FROM top_blocks tb
JOIN ws_rows wr ON wr.block_num = tb.block_num
ORDER BY tb.block_start DESC, wr.ws_event_count DESC, wr.workspace
