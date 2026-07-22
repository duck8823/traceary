-- Runtime mode is explicit for every session. Historical rows are classified
-- conservatively as interactive so they can never become eligible for
-- synthetic one-shot finalization solely because of this migration.
ALTER TABLE sessions ADD COLUMN runtime_mode TEXT NOT NULL DEFAULT 'interactive'
    CHECK (runtime_mode IN ('interactive', 'one_shot', 'resumed', 'background'));

-- Empty means the session is active or was ended by an older binary that did
-- not persist a reason. Existing ended rows are backfilled to an honest legacy
-- reason rather than fabricated success/failure.
ALTER TABLE sessions ADD COLUMN terminal_reason TEXT NOT NULL DEFAULT ''
    CHECK (terminal_reason IN ('', 'success', 'failure', 'timeout', 'signal', 'aborted_stream', 'legacy_unknown'));

UPDATE sessions
   SET terminal_reason = 'legacy_unknown'
 WHERE ended_at IS NOT NULL
   AND terminal_reason = '';
