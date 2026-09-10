-- Adds durable lifecycle state for Codex prompt-only consolidation (#2379).
-- The additive columns are compatible with older binaries. The backfill and partial
-- indexes require SQLite to visit existing ledger rows, so this migration is
-- data-dependent/offline; its cost has not been benchmarked.
ALTER TABLE consolidation_requests ADD COLUMN prompt_request_id TEXT;
ALTER TABLE consolidation_requests ADD COLUMN prompt_state TEXT NOT NULL DEFAULT 'pending'
    CHECK (prompt_state IN ('pending', 'claimed', 'delivered', 'fulfilled'));
ALTER TABLE consolidation_requests ADD COLUMN prompt_claim_token TEXT;
ALTER TABLE consolidation_requests ADD COLUMN prompt_claimed_at TEXT;
ALTER TABLE consolidation_requests ADD COLUMN prompt_lease_expires_at TEXT;
ALTER TABLE consolidation_requests ADD COLUMN prompt_delivered_at TEXT;
UPDATE consolidation_requests
   SET prompt_request_id = printf('consolidation:%s:%s', hex(session_id), hex(at_event_id))
 WHERE prompt_request_id IS NULL;
CREATE UNIQUE INDEX idx_consolidation_requests_prompt_request_id
    ON consolidation_requests(prompt_request_id);
CREATE INDEX idx_consolidation_requests_codex_prompt_pending
    ON consolidation_requests(session_id, id DESC)
 WHERE client = 'codex' AND delivery = 'none' AND refine_outcome IS NULL;
