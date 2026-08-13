-- Separate "contains synthesised text" (degraded) from "has agent reasoning
-- worth injecting at wake" (#1877). One row per session, so this cannot be
-- derived from prior generations. The UPDATE only touches session_refinements
-- (one row per session), not store-sized event tables.
ALTER TABLE session_refinements
    ADD COLUMN has_agent_reasoning INTEGER NOT NULL DEFAULT 0
    CHECK (has_agent_reasoning IN (0, 1));

UPDATE session_refinements
   SET has_agent_reasoning = CASE WHEN degraded = 0 THEN 1 ELSE 0 END;
