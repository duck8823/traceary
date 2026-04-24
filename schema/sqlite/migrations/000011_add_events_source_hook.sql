-- Add source_hook column so an event can record the exact host-side
-- hook event that produced it. Legacy rows stay NULL; readers treat
-- NULL as "unknown source" and keep working as before. See #672.
--
-- Host → source_hook vocabulary (stable identifiers so downstream
-- tools can filter without depending on client-specific capitalization):
--   claude:
--     session_start     — SessionStart
--     session_end       — SessionEnd
--     stop              — Stop  (also emits transcript + session_ended
--                        for Codex; source_hook disambiguates)
--     subagent_stop     — SubagentStop
--     pre_compact       — PreCompact
--     post_compact      — PostCompact
--     user_prompt_submit— UserPromptSubmit
--     post_tool_use     — PostToolUse
--     post_tool_use_failure — PostToolUseFailure
--   codex:
--     session_start     — SessionStart
--     stop              — Stop (fires transcript + session end pair)
--     user_prompt_submit— UserPromptSubmit
--     post_tool_use     — PostToolUse
--   gemini:
--     session_start     — SessionStart
--     session_end       — SessionEnd
--     after_agent       — AfterAgent (transcript)
--     after_tool        — AfterTool  (audit)
--
-- Empty / NULL source_hook remains valid for non-hook writes
-- (`traceary log`, MCP add_log, etc.).
ALTER TABLE events ADD COLUMN source_hook TEXT;

-- Readers can filter by source_hook cheaply; without an index large
-- histories would require a table scan just to answer
-- "all SubagentStop events in this workspace". Partial (WHERE
-- source_hook IS NOT NULL) keeps the index from indexing every legacy
-- NULL row.
CREATE INDEX idx_events_source_hook
    ON events(source_hook)
    WHERE source_hook IS NOT NULL;
