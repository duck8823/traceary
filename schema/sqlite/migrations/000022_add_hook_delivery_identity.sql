CREATE TABLE hook_deliveries (
    delivery_record_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    reported_delivery_id TEXT NOT NULL,
    delivery_fingerprint TEXT NOT NULL,
    identity_status TEXT NOT NULL
        CHECK (identity_status IN ('accepted', 'conflict')),
    observed_event_id TEXT NOT NULL,
    accepted_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT '',
    UNIQUE (session_id, reported_delivery_id, delivery_fingerprint)
);

CREATE UNIQUE INDEX idx_hook_deliveries_accepted_identity
    ON hook_deliveries(session_id, reported_delivery_id)
    WHERE identity_status = 'accepted';

CREATE TABLE session_workspace_aliases (
    session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
    alias_workspace TEXT NOT NULL,
    reviewed_at TEXT NOT NULL,
    reviewed_by TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, alias_workspace)
);

CREATE TABLE session_workspace_observations (
    observation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    raw_workspace TEXT,
    observation_kind TEXT NOT NULL
        CHECK (observation_kind IN ('primary', 'supplemental')),
    observation_origin TEXT NOT NULL
        CHECK (observation_origin IN ('runtime', 'backfill')),
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    observed_event_id TEXT,
    delivery_record_id TEXT,
    attribution_fingerprint TEXT NOT NULL,
    diagnostic_reason TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, observed_at DESC, session_id);

CREATE UNIQUE INDEX idx_session_workspace_observations_delivery_attribution
    ON session_workspace_observations(delivery_record_id, attribution_fingerprint)
    WHERE delivery_record_id IS NOT NULL AND delivery_record_id <> '';

CREATE UNIQUE INDEX idx_session_workspace_observations_primary_event
    ON session_workspace_observations(observed_event_id)
    WHERE observation_kind = 'primary'
      AND observed_event_id IS NOT NULL AND observed_event_id <> '';
