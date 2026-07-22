CREATE TABLE usage_observations (
    observation_id TEXT PRIMARY KEY NOT NULL CHECK (length(observation_id) BETWEEN 1 AND 512),
    session_id TEXT NOT NULL CHECK (length(session_id) > 0),
    host TEXT NOT NULL CHECK (length(host) > 0),
    source_name TEXT NOT NULL CHECK (length(source_name) > 0),
    source_version TEXT NOT NULL CHECK (length(source_version) > 0),
    provider TEXT,
    model TEXT,
    scope TEXT NOT NULL CHECK (scope IN ('call', 'run', 'session_snapshot')),
    accounting TEXT NOT NULL CHECK (accounting IN ('additive', 'latest_snapshot', 'excluded')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'finalized')),
    observed_at TEXT NOT NULL,
    finalized_at TEXT,
    terminal_code TEXT CHECK (terminal_code IN ('success', 'failure', 'timeout', 'signal', 'aborted_stream', 'unknown')),

    input_state TEXT NOT NULL CHECK (input_state IN ('unknown', 'unavailable', 'known')),
    input_tokens INTEGER,
    cached_input_state TEXT NOT NULL CHECK (cached_input_state IN ('unknown', 'unavailable', 'known')),
    cached_input_tokens INTEGER,
    cache_write_input_state TEXT NOT NULL CHECK (cache_write_input_state IN ('unknown', 'unavailable', 'known')),
    cache_write_input_tokens INTEGER,
    output_state TEXT NOT NULL CHECK (output_state IN ('unknown', 'unavailable', 'known')),
    output_tokens INTEGER,
    reasoning_output_state TEXT NOT NULL CHECK (reasoning_output_state IN ('unknown', 'unavailable', 'known')),
    reasoning_output_tokens INTEGER,
    total_state TEXT NOT NULL CHECK (total_state IN ('unknown', 'unavailable', 'known')),
    total_tokens INTEGER,

    cost_state TEXT NOT NULL CHECK (cost_state IN ('unknown', 'unavailable', 'known')),
    cost_amount_micros INTEGER,
    cost_currency TEXT,
    cost_origin TEXT CHECK (cost_origin IN ('estimated', 'provider_reported')),
    price_table_version TEXT,

    snapshot_series TEXT,
    snapshot_revision INTEGER,
    supersedes_id TEXT,

    CHECK ((input_state = 'known' AND input_tokens IS NOT NULL AND input_tokens >= 0)
        OR (input_state != 'known' AND input_tokens IS NULL)),
    CHECK ((cached_input_state = 'known' AND cached_input_tokens IS NOT NULL AND cached_input_tokens >= 0)
        OR (cached_input_state != 'known' AND cached_input_tokens IS NULL)),
    CHECK ((cache_write_input_state = 'known' AND cache_write_input_tokens IS NOT NULL AND cache_write_input_tokens >= 0)
        OR (cache_write_input_state != 'known' AND cache_write_input_tokens IS NULL)),
    CHECK ((output_state = 'known' AND output_tokens IS NOT NULL AND output_tokens >= 0)
        OR (output_state != 'known' AND output_tokens IS NULL)),
    CHECK ((reasoning_output_state = 'known' AND reasoning_output_tokens IS NOT NULL AND reasoning_output_tokens >= 0)
        OR (reasoning_output_state != 'known' AND reasoning_output_tokens IS NULL)),
    CHECK ((total_state = 'known' AND total_tokens IS NOT NULL AND total_tokens >= 0)
        OR (total_state != 'known' AND total_tokens IS NULL)),

    CHECK (
        (cost_state IN ('unknown', 'unavailable')
            AND cost_amount_micros IS NULL
            AND cost_currency IS NULL
            AND cost_origin IS NULL
            AND price_table_version IS NULL)
        OR
        (cost_state = 'known'
            AND cost_amount_micros IS NOT NULL
            AND cost_amount_micros >= 0
            AND cost_currency IS NOT NULL
            AND cost_currency GLOB '[A-Z][A-Z][A-Z]'
            AND cost_origin IS NOT NULL
            AND (
                (cost_origin = 'estimated'
                    AND price_table_version IS NOT NULL
                    AND length(trim(price_table_version)) > 0)
                OR (cost_origin = 'provider_reported' AND price_table_version IS NULL)
            ))
    ),

    CHECK (
        (scope = 'session_snapshot'
            AND accounting = 'latest_snapshot'
            AND status = 'finalized'
            AND snapshot_series IS NOT NULL
            AND length(snapshot_series) > 0
            AND snapshot_revision IS NOT NULL
            AND snapshot_revision >= 1)
        OR
        (scope IN ('call', 'run')
            AND accounting IN ('additive', 'excluded')
            AND snapshot_series IS NULL
            AND snapshot_revision IS NULL
            AND supersedes_id IS NULL)
    ),

    CHECK (
        (status = 'pending'
            AND finalized_at IS NULL
            AND terminal_code IS NULL
            AND input_state = 'unknown'
            AND cached_input_state = 'unknown'
            AND cache_write_input_state = 'unknown'
            AND output_state = 'unknown'
            AND reasoning_output_state = 'unknown'
            AND total_state = 'unknown'
            AND cost_state = 'unknown')
        OR
        (status = 'finalized'
            AND finalized_at IS NOT NULL
            AND terminal_code IS NOT NULL
            AND input_state != 'unknown'
            AND cached_input_state != 'unknown'
            AND cache_write_input_state != 'unknown'
            AND output_state != 'unknown'
            AND reasoning_output_state != 'unknown'
            AND total_state != 'unknown'
            AND cost_state != 'unknown')
    ),

    CHECK (supersedes_id IS NULL OR supersedes_id != observation_id),
    UNIQUE (snapshot_series, snapshot_revision),
    UNIQUE (observation_id, snapshot_series),
    FOREIGN KEY (supersedes_id, snapshot_series)
        REFERENCES usage_observations(observation_id, snapshot_series)
        ON DELETE RESTRICT
);

CREATE TRIGGER usage_observations_validate_snapshot_predecessor
BEFORE INSERT ON usage_observations
FOR EACH ROW
WHEN NEW.supersedes_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
          FROM usage_observations AS predecessor
         WHERE predecessor.observation_id = NEW.supersedes_id
           AND predecessor.snapshot_series = NEW.snapshot_series
           AND predecessor.scope = 'session_snapshot'
           AND predecessor.status = 'finalized'
           AND predecessor.snapshot_revision < NEW.snapshot_revision
    ) THEN RAISE(ABORT, 'invalid usage snapshot predecessor') END;
END;

CREATE UNIQUE INDEX idx_usage_observations_single_successor
    ON usage_observations(supersedes_id)
    WHERE supersedes_id IS NOT NULL;

CREATE UNIQUE INDEX idx_usage_observations_single_series_root
    ON usage_observations(snapshot_series)
    WHERE scope = 'session_snapshot' AND supersedes_id IS NULL;

CREATE INDEX idx_usage_observations_session_observed
    ON usage_observations(session_id, observed_at, observation_id);

CREATE INDEX idx_usage_observations_snapshot_series_revision
    ON usage_observations(snapshot_series, snapshot_revision DESC)
    WHERE scope = 'session_snapshot';

CREATE INDEX idx_usage_observations_aggregate
    ON usage_observations(status, accounting, observed_at, observation_id);
