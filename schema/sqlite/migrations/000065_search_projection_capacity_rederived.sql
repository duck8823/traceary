-- Persist whether this generation's source-text ceiling has been re-derived
-- from its own sample after the source→eviction transition (#1751).
-- The transition commit is no longer the only trigger: a crash between that
-- commit and the detached re-derivation write used to leave the Start-time
-- estimate in place forever. Default 0 retries once for in-flight eviction
-- rows after upgrade (we cannot know whether they already re-derived).

ALTER TABLE search_projection_state
    ADD COLUMN capacity_rederived INTEGER NOT NULL DEFAULT 0
        CHECK (capacity_rederived IN (0, 1));
