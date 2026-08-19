-- Drop raw-created_at events indexes superseded by the created_at_norm
-- family (migration 000031). Bounded: DROP INDEX moves pages to the freelist
-- (000059 precedent). Older binaries still open; they already have the norm
-- indexes for the same shapes.

DROP INDEX IF EXISTS idx_events_created_at;
DROP INDEX IF EXISTS idx_events_session_created_at;
DROP INDEX IF EXISTS idx_events_session_created_at_id_desc;
DROP INDEX IF EXISTS idx_events_workspace_created_at;
DROP INDEX IF EXISTS idx_events_source_hook_time;
