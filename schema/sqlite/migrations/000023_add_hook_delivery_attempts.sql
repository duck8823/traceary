CREATE TABLE hook_delivery_attempts (
    delivery_record_id TEXT NOT NULL,
    attempted_event_id TEXT NOT NULL,
    outcome TEXT NOT NULL
        CHECK (outcome IN ('accepted', 'conflict', 'exact_redelivery')),
    observed_at TEXT NOT NULL,
    PRIMARY KEY (delivery_record_id, attempted_event_id)
);

-- Seed the denominator for deliveries that were accepted before attempt
-- measurement existed. This copies identifiers and timestamps only; event
-- bodies never enter the diagnostic ledger.
INSERT INTO hook_delivery_attempts (
    delivery_record_id,
    attempted_event_id,
    outcome,
    observed_at
)
SELECT
    delivery_record_id,
    observed_event_id,
    identity_status,
    accepted_at
FROM hook_deliveries;

CREATE INDEX idx_hook_delivery_attempts_outcome
    ON hook_delivery_attempts(outcome, observed_at DESC, delivery_record_id);
