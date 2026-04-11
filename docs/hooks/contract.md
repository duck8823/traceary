# Hook Contract

[日本語](./contract.ja.md)

This document defines the hook capability tiers across AI agent clients.

## Capability Tiers

### Tier 1: Full (Claude Code)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | `*` | Record session start |
| SessionEnd | `*` | Record session end |
| PostToolUse | `Bash` | Record shell command audit with exit code |
| PostToolUse | `mcp__.*` | Record MCP tool audit |
| PostToolUseFailure | `Bash`, `mcp__.*` | Record failed tool execution |
| PostCompact | `*` | Record compact summary |
| UserPromptSubmit | `*` | Record user prompt text |
| SessionStart (compact) (planned) | `compact` | Inject context pointer via stdout |

### Tier 2: Partial (Codex)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | (all) | Record session start |
| Stop | (all) | Record session end |
| PostToolUse | (all) | Record tool audit |

**Limitations**: No SessionEnd (uses Stop instead), no compact hooks, no failure-specific event.

### Tier 3: Basic (Gemini CLI)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | `*` | Record session start |
| SessionEnd | `*` | Record session end |
| AfterTool | `*` | Record tool audit |

**Limitations**: No compact hooks, no failure-specific event, no PostCompact/SessionStart(compact).

## Shared Behavior

All tiers:
- Session start persists repo to state file; audit reads repo from state
- Agent type resolution: `agent_type` field → hierarchical agent name (Claude only)
- Exit code extraction from `tool_response.exitCode` when available
- MCP tool name fallback: `tool_input.command` → `tool_name`

## Fallback for Missing Capabilities

| Missing Capability | Fallback |
|---|---|
| Compact hooks | MCP `get_context` / `session_handoff` on demand |
| Failure event | Parse exit code from tool_response in audit script |
| Agent type | Use client name only (e.g., `codex`, `gemini`) |
