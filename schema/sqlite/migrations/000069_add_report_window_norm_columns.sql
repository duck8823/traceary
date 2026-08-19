-- Additive report-window indexes. Function wrappers on stored columns
-- (`ts_norm(started_at)`) cannot use an index; persist the same lexical form
-- as events.created_at_norm. Previous binaries ignore the new columns
-- (DEFAULT ''). Row backfill is batched at store open, not in this file.

ALTER TABLE sessions ADD COLUMN started_at_norm TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_observations ADD COLUMN observed_at_norm TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_sessions_started_at_norm_id_desc
  ON sessions(started_at_norm DESC, session_id DESC);

-- status equality plus the window/keyset order. A time-only index loses to
-- idx_usage_observations_aggregate (status) and then sorts.
CREATE INDEX idx_usage_observations_status_observed_at_norm_id_desc
  ON usage_observations(status, observed_at_norm DESC, observation_id DESC);

CREATE TRIGGER sessions_started_at_norm_ai AFTER INSERT ON sessions
WHEN NEW.started_at_norm = ''
BEGIN
  UPDATE sessions SET started_at_norm = ts_norm(NEW.started_at) WHERE session_id = NEW.session_id;
END;

CREATE TRIGGER sessions_started_at_norm_au AFTER UPDATE OF started_at ON sessions
BEGIN
  UPDATE sessions SET started_at_norm = ts_norm(NEW.started_at) WHERE session_id = NEW.session_id;
END;

CREATE TRIGGER usage_observations_observed_at_norm_ai AFTER INSERT ON usage_observations
WHEN NEW.observed_at_norm = ''
BEGIN
  UPDATE usage_observations SET observed_at_norm = ts_norm(NEW.observed_at) WHERE observation_id = NEW.observation_id;
END;

CREATE TRIGGER usage_observations_observed_at_norm_au AFTER UPDATE OF observed_at ON usage_observations
BEGIN
  UPDATE usage_observations SET observed_at_norm = ts_norm(NEW.observed_at) WHERE observation_id = NEW.observation_id;
END;
