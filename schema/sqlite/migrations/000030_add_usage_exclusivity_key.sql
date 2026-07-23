ALTER TABLE usage_observations
    ADD COLUMN exclusivity_key TEXT CHECK (
        exclusivity_key IS NULL OR (
            length(CAST(exclusivity_key AS BLOB)) BETWEEN 1 AND 512
            AND length(trim(exclusivity_key, char(9, 10, 11, 12, 13, 32))) > 0
        )
    );

-- Migration 29 was exercised by pre-release development builds. Preserve its
-- durable winner facts while moving the portable identity onto observations.
UPDATE usage_observations
   SET exclusivity_key = (
       SELECT claim.claim_key
         FROM usage_exclusivity_claims AS claim
        WHERE claim.winner_observation_id = usage_observations.observation_id
   )
 WHERE observation_id IN (
       SELECT winner_observation_id FROM usage_exclusivity_claims
   );

-- A supervised headless observation ID is itself the normalized key. This
-- also recovers alternatives written before the legacy winner claim existed.
UPDATE usage_observations
   SET exclusivity_key = observation_id
 WHERE exclusivity_key IS NULL
   AND host = 'codex'
   AND source_name = 'headless_stream'
   AND observation_id LIKE 'codex:headless_stream:%'
   AND accounting IN ('additive', 'excluded');

CREATE UNIQUE INDEX idx_usage_observations_additive_exclusivity
    ON usage_observations(exclusivity_key)
    WHERE exclusivity_key IS NOT NULL
      AND accounting = 'additive';

CREATE INDEX idx_usage_observations_exclusivity
    ON usage_observations(exclusivity_key)
    WHERE exclusivity_key IS NOT NULL;

-- The repository may attach a portable key once to a legacy excluded
-- alternative that migration 29 could not map. The key is immutable afterward.
CREATE TRIGGER usage_observations_reject_exclusivity_key_update
BEFORE UPDATE OF exclusivity_key ON usage_observations
FOR EACH ROW
WHEN OLD.exclusivity_key IS NOT NULL OR NEW.exclusivity_key IS NULL
BEGIN
    SELECT RAISE(ABORT, 'usage observation exclusivity key is immutable');
END;
