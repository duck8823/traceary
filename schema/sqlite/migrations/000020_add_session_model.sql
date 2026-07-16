-- Host-reported model identifier for a session (opaque host string).
-- Empty means the host did not report a model (Gemini/Antigravity/CLI/MCP).
ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT '';
