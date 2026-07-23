CREATE TABLE usage_exclusivity_claims (
    claim_key TEXT PRIMARY KEY NOT NULL CHECK (
        length(CAST(claim_key AS BLOB)) BETWEEN 1 AND 512
        AND length(trim(claim_key, char(9, 10, 11, 12, 13, 32))) > 0
    ),
    winner_observation_id TEXT NOT NULL UNIQUE,
    FOREIGN KEY (winner_observation_id)
        REFERENCES usage_observations(observation_id)
        ON DELETE RESTRICT
);

CREATE TRIGGER usage_exclusivity_claims_reject_update
BEFORE UPDATE ON usage_exclusivity_claims
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage exclusivity claim is immutable');
END;

CREATE TRIGGER usage_exclusivity_claims_reject_delete
BEFORE DELETE ON usage_exclusivity_claims
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage exclusivity claim is immutable');
END;
