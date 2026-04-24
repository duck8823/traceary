-- Replace the simple source_hook partial index (#672) with a compound
-- `(source_hook, created_at DESC, id DESC)` partial index so the
-- `traceary list --source-hook <name>` SQL (now dispatched via a
-- dedicated query with `e.source_hook = ?` as a top-level AND
-- conjunct) can use a COVERING scan instead of falling back to
-- `idx_events_created_at` and filtering in memory. See #683.
--
-- The index stays PARTIAL (`WHERE source_hook IS NOT NULL`) so NULL
-- rows (non-hook writes) do not bloat the index.
DROP INDEX IF EXISTS idx_events_source_hook;

CREATE INDEX idx_events_source_hook_time
    ON events(source_hook, created_at DESC, id DESC)
    WHERE source_hook IS NOT NULL;
