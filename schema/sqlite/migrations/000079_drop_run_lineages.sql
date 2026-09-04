-- 000079_drop_run_lineages.sql
--
-- Drops run_lineages after rebuilding usage_observation_runs without the
-- run_lineages foreign key. SQLite cannot DROP CONSTRAINT; DROP TABLE of the
-- parent fails with FOREIGN KEY constraint failed (19) when child rows exist.
-- Data-dependent: classified MigrationDataDependentOffline. Applied by
-- `traceary doctor --fix` on a run-owned candidate, never at live open.
--
-- Raises minimum_reader_version to 35 so older binaries fail loudly.
-- validate_insert is not recreated: it joined the dropped table.

DROP TRIGGER IF EXISTS usage_observation_runs_validate_insert;
DROP TRIGGER IF EXISTS usage_observation_runs_reject_update;
DROP TRIGGER IF EXISTS usage_observation_runs_reject_delete;

CREATE TABLE usage_observation_runs_v2 (
    observation_id TEXT PRIMARY KEY NOT NULL,
    run_host TEXT NOT NULL,
    run_id TEXT NOT NULL,
    FOREIGN KEY (observation_id)
        REFERENCES usage_observations(observation_id)
        ON DELETE RESTRICT
);

INSERT INTO usage_observation_runs_v2 (observation_id, run_host, run_id)
SELECT observation_id, run_host, run_id FROM usage_observation_runs;

DROP TABLE usage_observation_runs;

ALTER TABLE usage_observation_runs_v2 RENAME TO usage_observation_runs;

CREATE TRIGGER usage_observation_runs_reject_update
BEFORE UPDATE ON usage_observation_runs
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage observation run attribution is immutable');
END;

CREATE TRIGGER usage_observation_runs_reject_delete
BEFORE DELETE ON usage_observation_runs
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'usage observation run attribution is immutable');
END;

CREATE INDEX idx_usage_observation_runs_identity
    ON usage_observation_runs(run_host, run_id);

DROP TABLE run_lineages;

UPDATE store_format_state SET minimum_reader_version = 35 WHERE singleton = 1;
