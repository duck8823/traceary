-- consolidation_requests is the measurement ledger for the stop-hook fold
-- request (#1674, #2273). One row per emitted request. It is bookkeeping about
-- Traceary's own behaviour, not session content, which is why it is a table and
-- not an events row: an events row would appear in the "latest event to cover"
-- reasoning the refine skill hands the agent, perturbing the very signal the
-- trigger measures.
--
-- requested_at is stored in the FIXED-WIDTH canonical form that ts_norm emits
-- (see formatMemoryValidityTimestamp / normalizeRFC3339NanoForCompare), NOT the
-- variable-width RFC3339Nano that formatTimestamp emits. Variable-width is not
-- lexically ordered ('.' 0x2E < 'Z' 0x5A), so a plain TEXT range scan over it is
-- wrong near a fractional-second boundary (#1185).
CREATE TABLE consolidation_requests (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id            TEXT    NOT NULL,
    client                TEXT    NOT NULL,
    requested_at          TEXT    NOT NULL,
    at_event_id           TEXT    NOT NULL,
    signal                TEXT    NOT NULL,
    pressure_value        INTEGER NOT NULL,
    threshold_value       INTEGER NOT NULL,
    re_request            INTEGER NOT NULL CHECK (re_request IN (0, 1)),
    delivery              TEXT    NOT NULL CHECK (delivery IN ('stop_exit_2', 'additional_context', 'none')),
    refine_outcome        TEXT    CHECK (refine_outcome IN ('accepted', 'rejected')),
    refine_reason         TEXT    NOT NULL DEFAULT '',
    refine_produced_by    TEXT    NOT NULL DEFAULT '',
    refined_at            TEXT,
    refinement_generation INTEGER,
    UNIQUE (session_id, at_event_id)
);

CREATE INDEX idx_consolidation_requests_session_id_desc
    ON consolidation_requests(session_id, id DESC);
CREATE INDEX idx_consolidation_requests_requested_at_client
    ON consolidation_requests(requested_at, client);
