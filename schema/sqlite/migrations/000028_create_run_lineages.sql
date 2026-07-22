CREATE TABLE run_lineages (
    host TEXT NOT NULL CHECK (
        length(CAST(host AS BLOB)) > 0
        AND length(trim(host, char(9, 10, 11, 12, 13, 32))) > 0
    ),
    run_id TEXT NOT NULL CHECK (
        length(CAST(run_id AS BLOB)) BETWEEN 1 AND 512
        AND length(trim(run_id, char(9, 10, 11, 12, 13, 32))) > 0
    ),
    parent_host TEXT,
    parent_run_id TEXT,
    session_id TEXT,
    batch_id TEXT,
    ticket_ref TEXT,
    repository TEXT,
    pull_request_number INTEGER,
    head_sha TEXT,
    packet_sha256 TEXT,
    packet_bytes INTEGER,
    tool_output_bytes INTEGER,

    PRIMARY KEY (host, run_id),
    CHECK ((parent_host IS NULL AND parent_run_id IS NULL)
        OR (parent_host IS NOT NULL AND parent_run_id IS NOT NULL)),
    CHECK (parent_host IS NULL OR parent_host != host OR parent_run_id != run_id),
    CHECK (parent_host IS NULL OR length(CAST(parent_host AS BLOB)) > 0),
    CHECK (parent_run_id IS NULL OR (
        length(CAST(parent_run_id AS BLOB)) BETWEEN 1 AND 512
        AND length(trim(parent_run_id, char(9, 10, 11, 12, 13, 32))) > 0
    )),
    CHECK (session_id IS NULL OR length(CAST(session_id AS BLOB)) > 0),
    CHECK (batch_id IS NULL OR (
        length(CAST(batch_id AS BLOB)) BETWEEN 1 AND 512
        AND length(trim(batch_id, char(9, 10, 11, 12, 13, 32))) > 0
    )),
    CHECK (ticket_ref IS NULL OR (
        length(CAST(ticket_ref AS BLOB)) BETWEEN 1 AND 512
        AND length(trim(ticket_ref, char(9, 10, 11, 12, 13, 32))) > 0
    )),
    CHECK (repository IS NULL OR (
        length(CAST(repository AS BLOB)) BETWEEN 1 AND 2048
        AND length(trim(repository, char(9, 10, 11, 12, 13, 32))) > 0
    )),
    CHECK (pull_request_number IS NULL OR (pull_request_number > 0 AND repository IS NOT NULL)),
    CHECK (head_sha IS NULL OR (
        repository IS NOT NULL
        AND length(head_sha) IN (40, 64)
        AND head_sha NOT GLOB '*[^0-9a-f]*'
    )),
    CHECK ((packet_sha256 IS NULL AND packet_bytes IS NULL)
        OR (packet_sha256 IS NOT NULL
            AND length(packet_sha256) = 64
            AND packet_sha256 NOT GLOB '*[^0-9a-f]*'
            AND packet_bytes IS NOT NULL
            AND packet_bytes >= 0)),
    CHECK (tool_output_bytes IS NULL OR tool_output_bytes >= 0),
    FOREIGN KEY (parent_host, parent_run_id)
        REFERENCES run_lineages(host, run_id)
        ON DELETE RESTRICT
);

CREATE TRIGGER run_lineages_reject_update
BEFORE UPDATE ON run_lineages
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'run lineage is immutable');
END;

CREATE TRIGGER run_lineages_reject_delete
BEFORE DELETE ON run_lineages
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'run lineage is immutable');
END;

CREATE INDEX idx_run_lineages_parent
    ON run_lineages(parent_host, parent_run_id)
    WHERE parent_host IS NOT NULL;

CREATE INDEX idx_run_lineages_session
    ON run_lineages(session_id)
    WHERE session_id IS NOT NULL;

CREATE TABLE usage_observation_runs (
    observation_id TEXT PRIMARY KEY NOT NULL,
    run_host TEXT NOT NULL,
    run_id TEXT NOT NULL,
    FOREIGN KEY (observation_id)
        REFERENCES usage_observations(observation_id)
        ON DELETE RESTRICT,
    FOREIGN KEY (run_host, run_id)
        REFERENCES run_lineages(host, run_id)
        ON DELETE RESTRICT
);

CREATE TRIGGER usage_observation_runs_validate_insert
BEFORE INSERT ON usage_observation_runs
FOR EACH ROW
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
          FROM usage_observations AS observation
          JOIN run_lineages AS run
            ON run.host = NEW.run_host
           AND run.run_id = NEW.run_id
         WHERE observation.observation_id = NEW.observation_id
           AND observation.scope IN ('call', 'run')
           AND observation.host = run.host
           AND (run.session_id IS NULL OR run.session_id = observation.session_id)
    ) THEN RAISE(ABORT, 'invalid usage observation run attribution') END;
END;

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
