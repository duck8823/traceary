-- The gc-only narrowing of the shared allowlist: the covering refinement must
-- already have existed when this gc run started.
--
-- gc consolidates orphan ranges before it discards, and --dry-run consolidates
-- without writing. Without this bound the preview would count only the folds
-- that already existed while the apply discarded those plus everything it had
-- just folded — the preview would understate an irreversible loss, worst on
-- the first run after upgrade. Bounding by the run start makes the two agree
-- exactly, and each run still discards everything folded by earlier runs, so
-- the discard never stalls.
--
-- sessions.session_id is the primary key of session_refinements, so at most
-- one refinement can cover the event the inner query already validated.
--
-- store retention composes the shared allowlist directly instead: it folds
-- nothing, so every refinement it sees predates it.
SELECT d.id
  FROM (
-- discardable-event-bodies
  ) AS d
  JOIN events AS e ON e.id = d.id
  JOIN session_refinements AS r ON r.session_id = e.session_id
 WHERE ts_valid(r.produced_at)
   AND ts_norm(r.produced_at) < ts_norm(?)
