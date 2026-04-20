ALTER TABLE memories ADD COLUMN valid_from TEXT;
ALTER TABLE memories ADD COLUMN valid_to TEXT;

-- Backfill valid_from from created_at so existing memories behave as
-- "valid from the moment they were recorded". valid_to stays NULL so
-- existing memories remain open-ended (no auto-expiry).
UPDATE memories
SET valid_from = created_at
WHERE valid_from IS NULL;

-- Composite index supporting "retrieve memories that are still valid
-- as of a given timestamp". valid_to NULL sorts last (open-ended).
CREATE INDEX idx_memories_valid_window
    ON memories(valid_to, valid_from);
