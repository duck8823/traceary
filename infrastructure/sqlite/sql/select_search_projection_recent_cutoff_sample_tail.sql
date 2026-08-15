-- Oldest created_at_norm among the newest N events, plus how many rows the
-- sample actually held. Caller uses this when the window query finds no
-- crossing: sampled < N means the whole corpus fits; sampled = N means the
-- prefilter admits only this window (#1807).
SELECT COUNT(*), COALESCE(MIN(created_at_norm), '')
  FROM (
    SELECT e.created_at_norm AS created_at_norm
      FROM events e
     ORDER BY e.created_at_norm DESC, e.id DESC
     LIMIT ?
  ) newest
