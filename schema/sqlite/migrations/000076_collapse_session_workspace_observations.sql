-- 000076_collapse_session_workspace_observations.sql
--
-- session_workspace_observations was append-only: one row per delivered event
-- plus supplemental redeliveries plus backfill rows, and nothing deleted them.
-- Live store 2026-08-28: 846,713 rows / 0.37 GiB against 614,864 events,
-- +82,554 rows per 7 days. Collapse to one row per
-- (session_id, workspace, observed_relationship, source_client, source_hook,
--  observation_kind) with a volume counter, so the family grows with distinct
-- attribution keys instead of with events (#2269).
--
-- Measured on a copy of the operator store before this migration was written:
--   rows=846713  (keys recorded at purpose-gate on a fresh copy)
--
-- Data-dependent: this rewrites the whole family. Registered
-- MigrationDataDependentOffline in prepared_migration_catalog.go; it is applied
-- by `traceary doctor --fix`, never implicitly on live open.
--
-- DROP TABLE removes idx_session_workspace_observations_relationship,
-- idx_session_workspace_observations_delivery_attribution, and
-- idx_session_workspace_observations_primary_event together. Only the
-- relationship index is recreated: after collapse, observed_event_id and
-- (delivery_record_id, attribution_fingerprint) are no longer unique.

CREATE TABLE session_workspace_observations_collapsed (
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT '',
    observation_kind TEXT NOT NULL
        CHECK (observation_kind IN ('primary', 'supplemental')),
    observation_count INTEGER NOT NULL DEFAULT 1 CHECK (observation_count >= 1),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    observed_event_id TEXT,
    raw_workspace TEXT,
    delivery_record_id TEXT,
    attribution_fingerprint TEXT NOT NULL,
    diagnostic_reason TEXT NOT NULL DEFAULT '',
    observation_origin TEXT NOT NULL
        CHECK (observation_origin IN ('runtime', 'backfill')),
    PRIMARY KEY (session_id, workspace, observed_relationship, source_client, source_hook, observation_kind)
) WITHOUT ROWID;

-- One ordered pass with window ranks, not a correlated subquery per key.
-- first/last observed_at keep the original stored strings selected in
-- ts_norm order (do not MIN()/MAX() RFC3339 text; variable fractional
-- seconds break lexicographic compare, #1185). Correlated first/last
-- subqueries stalled for >20 min on an 856k-row copy.

INSERT INTO session_workspace_observations_collapsed
WITH ranked AS (
    SELECT session_id, workspace, observed_relationship, source_client, source_hook, observation_kind,
           observed_at, observed_event_id, raw_workspace, delivery_record_id,
           attribution_fingerprint, diagnostic_reason,
           COUNT(*) OVER (
             PARTITION BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
           ) AS observation_count,
           CASE WHEN MAX(CASE WHEN observation_origin = 'runtime' THEN 1 ELSE 0 END) OVER (
             PARTITION BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
           ) = 1 THEN 'runtime' ELSE 'backfill' END AS observation_origin,
           ROW_NUMBER() OVER (
             PARTITION BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
             ORDER BY ts_norm(observed_at) DESC, observation_id DESC
           ) AS rn_desc,
           ROW_NUMBER() OVER (
             PARTITION BY session_id, workspace, observed_relationship, source_client, source_hook, observation_kind
             ORDER BY ts_norm(observed_at) ASC, observation_id ASC
           ) AS rn_asc
      FROM session_workspace_observations
)
SELECT last.session_id, last.workspace, last.observed_relationship, last.source_client, last.source_hook, last.observation_kind,
       last.observation_count, first.observed_at, last.observed_at,
       last.observed_event_id, last.raw_workspace, last.delivery_record_id,
       last.attribution_fingerprint, last.diagnostic_reason, last.observation_origin
  FROM ranked last
  JOIN ranked first
    ON first.rn_asc = 1
   AND last.rn_desc = 1
   AND first.session_id = last.session_id AND first.workspace = last.workspace
   AND first.observed_relationship = last.observed_relationship
   AND first.source_client = last.source_client AND first.source_hook = last.source_hook
   AND first.observation_kind = last.observation_kind;

DROP TABLE session_workspace_observations;
ALTER TABLE session_workspace_observations_collapsed RENAME TO session_workspace_observations;

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, last_observed_at DESC, session_id);

-- Catch-up frontier. Descending sweep: everything strictly newer than the
-- frontier has already been examined; '' means no sweep has started.
-- exhausted is intentionally left untouched.

ALTER TABLE workspace_observation_catchup_state
    ADD COLUMN frontier_created_at_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE workspace_observation_catchup_state
    ADD COLUMN frontier_event_id TEXT NOT NULL DEFAULT '';
