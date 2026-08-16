-- Track consecutive cleanup-phase attempts that commit no row, so automatic
-- catch-up can park a generation whose cleanup phase cannot make forward
-- progress instead of retrying the same unwinnable unit of work on every
-- store open (#2010). Reset to 0 whenever a cleanup attempt does make
-- progress; incremented on each no-progress attempt.

ALTER TABLE search_projection_state
    ADD COLUMN cleanup_no_progress_attempts INTEGER NOT NULL DEFAULT 0;
