-- 000078_repair_epoch_zero_hook_usage.sql
--
-- One-off repair of stop-hook-family and headless_stream usage_observations
-- whose observed_at is Unix epoch (the historical Go catch-up
-- catchUpEpochZeroHookUsage). Cost scales with dirty rows and the rewrite
-- must DROP/CREATE usage_observations_reject_descriptor_update, so this is
-- MigrationDataDependentOffline — applied by `traceary doctor --fix`, never
-- on live hook open (#2316).
--
-- Parity with the retired Go body: candidate timestamps are selected with
-- the same COALESCE as usage_epoch_repair.go, then fail-closed with
-- ts_valid(trim(stamp)) plus ts_norm(trim(stamp)) >= the first second after
-- epoch, matching time.Parse(RFC3339Nano) && Unix() > 0. ts_norm alone
-- degrades malformed input to lexical passthrough and would otherwise
-- rewrite rows the Go guard skipped.
--
-- Unrepairable epoch-zero rows (no usable session/event timestamp) are left
-- unchanged, as the Go repair skipped them.

DROP TRIGGER IF EXISTS usage_observations_reject_descriptor_update;

UPDATE usage_observations
   SET observed_at = repair.stamp,
       finalized_at = CASE
           WHEN usage_observations.finalized_at IS NOT NULL
            AND ts_norm(usage_observations.finalized_at) = '1970-01-01T00:00:00.000000000Z'
           THEN repair.stamp
           ELSE usage_observations.finalized_at
       END
  FROM (
    SELECT observation.observation_id AS observation_id,
           COALESCE(
               (SELECT event.created_at
                  FROM events AS event
                 WHERE event.session_id = observation.session_id
                   AND event.kind IN ('session_ended', 'session_started')
                 ORDER BY ts_norm(event.created_at) DESC, event.id DESC
                 LIMIT 1),
               (SELECT event.created_at
                  FROM events AS event
                 WHERE event.session_id = observation.session_id
                 ORDER BY ts_norm(event.created_at) DESC, event.id DESC
                 LIMIT 1),
               (SELECT session.ended_at
                  FROM sessions AS session
                 WHERE session.session_id = observation.session_id
                   AND session.ended_at IS NOT NULL
                   AND length(trim(session.ended_at)) > 0),
               (SELECT session.started_at
                  FROM sessions AS session
                 WHERE session.session_id = observation.session_id)
           ) AS stamp
      FROM usage_observations AS observation
     WHERE ts_norm(observation.observed_at) = '1970-01-01T00:00:00.000000000Z'
       AND observation.source_name IN ('stop_hook', 'session_end_hook', 'after_agent_hook', 'headless_stream')
  ) AS repair
 WHERE usage_observations.observation_id = repair.observation_id
   AND repair.stamp IS NOT NULL
   AND ts_valid(trim(repair.stamp)) = 1
   AND ts_norm(trim(repair.stamp)) >= '1970-01-01T00:00:01.000000000Z';

CREATE TRIGGER usage_observations_reject_descriptor_update
BEFORE UPDATE OF observation_id, session_id, host, source_name, source_version,
    provider, model, scope, accounting, observed_at, snapshot_series,
    snapshot_revision, supersedes_id
ON usage_observations
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage observation descriptor is immutable');
END;
