-- Express the search index budget in physical index-family bytes (#1679).
-- Renames the operator-facing ceiling and records the measured derivation
-- (amplification, non-recent reserve, source ceiling, cutoff, evidence).
-- capacity_semantics_version defaults to 1 so existing generations are
-- obsolete under the new semantics and CatchUp replaces them.
-- index_family_within_budget: -1 = not yet measured, 0 = over, 1 = within.

ALTER TABLE search_projection_state RENAME COLUMN recent_byte_limit TO index_family_byte_limit;
ALTER TABLE search_projection_state ADD COLUMN capacity_semantics_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE search_projection_state ADD COLUMN recent_source_ceiling_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_projection_state ADD COLUMN recent_amplification_ppm INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_projection_state ADD COLUMN non_recent_family_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_projection_state ADD COLUMN recent_cutoff_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN capacity_evidence_status TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN capacity_evidence_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_state ADD COLUMN index_family_within_budget INTEGER NOT NULL DEFAULT -1;
