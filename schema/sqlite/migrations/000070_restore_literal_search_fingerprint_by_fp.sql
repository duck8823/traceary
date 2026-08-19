-- Restore a fingerprint-first access path. Migration 000059 dropped
-- idx_literal_search_fingerprint_candidate because the then-readers filtered
-- on the primary-key prefix (generation_id, event_id, fingerprint). The
-- #2127 candidate set starts at fingerprint IN (…) so that index is needed
-- again. CREATE INDEX does not rewrite fingerprint rows.

CREATE INDEX IF NOT EXISTS idx_literal_search_fingerprints_by_fp
  ON literal_search_fingerprints(generation_id, fingerprint_version, fingerprint, event_id);
