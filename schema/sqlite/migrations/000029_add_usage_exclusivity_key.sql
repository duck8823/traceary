ALTER TABLE usage_observations
    ADD COLUMN exclusivity_key TEXT CHECK (
        exclusivity_key IS NULL OR (
            length(CAST(exclusivity_key AS BLOB)) BETWEEN 1 AND 512
            AND length(trim(exclusivity_key, char(9, 10, 11, 12, 13, 32))) > 0
        )
    );

-- Development builds before the v0.32.0 release could persist supervised
-- headless observations while this migration was still being reviewed. Their
-- authoritative observation ID is already the normalized exclusivity key.
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
