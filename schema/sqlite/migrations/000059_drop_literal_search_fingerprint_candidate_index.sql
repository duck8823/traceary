-- Drop an unused access path; rows and query semantics are unchanged, so no generation needs invalidating.
DROP INDEX IF EXISTS idx_literal_search_fingerprint_candidate;
