-- First-ask replay of the work-based consolidation trigger (cadence ignored).
-- Same predicates as issue #2274 purpose-gate: August 2026 main sessions,
-- command_executed counts, no event bodies, no ts_norm().
-- The purpose-gate groups this by client; the agreement test reads one row
-- per session. would_ask is 1 when commands >= 20 (default min_commands).
WITH main AS (
  SELECT session_id, client
    FROM sessions
   WHERE started_at >= '2026-08-01' AND started_at < '2026-09-01'
     AND parent_session_id IS NULL
     AND subagent_kind = ''
     AND instr(agent, '/') = 0
),
work AS (
  SELECT e.session_id, COUNT(*) AS commands
    FROM events AS e
    JOIN main AS m ON m.session_id = e.session_id
   WHERE e.kind = 'command_executed'
   GROUP BY e.session_id
)
SELECT m.session_id AS session_id,
       m.client AS client,
       COALESCE(w.commands, 0) AS commands,
       CASE WHEN COALESCE(w.commands, 0) >= 20 THEN 1 ELSE 0 END AS would_ask
  FROM main AS m
  LEFT JOIN work AS w ON w.session_id = m.session_id
 ORDER BY m.session_id;
