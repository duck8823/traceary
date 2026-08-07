-- raw_body_retention_entries is keyed (plan_id, event_id), so asking "does this
-- event carry a ledger entry?" -- the question the content-event dedupe repair
-- asks once per candidate row -- has no usable index and degrades to a full scan
-- of the ledger per row. At the repair's working size (246,657 candidates) that
-- is quadratic on the one scan the repair exists to make runnable.
--
-- Building the index costs time proportional to the ledger's row count, so this
-- migration is classified data_dependent_offline in the prepared-migration
-- catalog, like 000035, and unlike the table-creating migrations around it.
CREATE INDEX IF NOT EXISTS idx_raw_body_retention_entries_event_id
    ON raw_body_retention_entries(event_id);
