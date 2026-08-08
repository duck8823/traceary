-- Distinguish "the projection family measured zero bytes" from "the
-- measurement could not be taken". Migration 049 recorded only the byte
-- figures, so an unavailable dbstat walk and a genuinely empty family were
-- indistinguishable at zero.
--
-- Before and after are measured at different times, by different transactions,
-- against families of different sizes, so each carries its own evidence. A
-- single shared status would let a successful after-measurement report the
-- whole row as measured while the before figure was never taken.
--
-- The measurement is diagnostic and runs outside the transactions that start
-- and complete a generation: a dbstat walk costs in proportion to the
-- projection family's page count and must never be able to abort a completion
-- that is otherwise durable.
ALTER TABLE search_projection_state ADD COLUMN cutover_before_evidence_status TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN cutover_before_evidence_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN cutover_after_evidence_status TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN cutover_after_evidence_reason TEXT NOT NULL DEFAULT '';
