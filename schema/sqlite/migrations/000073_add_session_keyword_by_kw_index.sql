-- Filterable session search starts at keyword equality. The table PK is
-- (generation_id, session_id, keyword), so keyword is not a seek prefix.
-- CREATE INDEX does not rewrite keyword rows. Previous binaries ignore it.

CREATE INDEX IF NOT EXISTS idx_search_projection_session_keywords_by_kw
  ON search_projection_session_keywords(generation_id, keyword, session_id);
