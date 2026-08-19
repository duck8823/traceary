-- Bookkeeping only: after a 0-row catch-up batch, later Initialize skips the
-- events anti-join. Compact may leave orphan observations, so COUNT(events)
-- versus COUNT(observations) is not a completeness signal. No event-insert
-- trigger: live Save writes the primary observation itself.

CREATE TABLE workspace_observation_catchup_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    exhausted INTEGER NOT NULL
);

INSERT INTO workspace_observation_catchup_state(singleton, exhausted) VALUES (1, 0);
