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
| PostToolUse | `Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode` | Record built-in Claude Code tool invocations (file I/O, search, agent, web, plan-mode exit). Added in v0.8-6; expanded to include `NotebookRead` and `ExitPlanMode` in v0.8-6b |
| PostToolUseFailure | `Bash`, `mcp__.*`, built-in tools | Record failed tool execution |
| PostCompact | `*` | Record compact summary |
| UserPromptSubmit | `*` | Record user prompt text |
| Stop | `*` | Record last assistant message from `transcript_path` as a `transcript` event (built-in secret redaction + operator-configured `redact.extra_patterns` applied) |
| SessionStart (compact) (planned) | `compact` | Inject context pointer via stdout |

### Tier 2: Partial (Codex)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | (all) | Record session start |
| UserPromptSubmit | (all) | Record user prompt text |
| Stop | (all) | Record session end and the final assistant message (from `last_assistant_message`) as a `transcript` event (built-in secret redaction + operator-configured `redact.extra_patterns` applied) |
| PostToolUse | (all) | Record tool audit |

**Limitations**: No SessionEnd (uses Stop instead), no compact hooks, no failure-specific event.

### Tier 3: Basic (Gemini CLI)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | `*` | Record session start |
| SessionEnd | `*` | Record session end |
| AfterAgent | `*` | Record the agent response (from `prompt_response`) as a `transcript` event (built-in secret redaction + operator-configured `redact.extra_patterns` applied) |
| AfterTool | `*` | Record tool audit |

**Limitations**: No compact hooks, no failure-specific event, no PostCompact/SessionStart(compact). Gemini has no Stop event, so transcript capture is attached to `AfterAgent` instead.

## Shared Behavior

All tiers:
- Session start persists the resolved workspace to the state file; audit reads the workspace from state
- Agent type resolution: `agent_type` field → hierarchical agent name (Claude only)
- Exit code extraction from `tool_response.exitCode` when available
- MCP tool name fallback: `tool_input.command` → `tool_name`

Claude Task subagent capture:
- `PreToolUse:Task` opens a child session under the currently active immediate parent. If that child starts another Task, Traceary links the grandchild to the child rather than to the top-level session.
- Active Task state is tracked per parent session, so sibling spawn order is allocated independently within each `parent_session_id`.
- If `tool_use_id` is missing, Traceary synthesizes a stable Task key from `event_id`. If a later `PostToolUse` / `SubagentStop` omits `tool_use_id`, Traceary falls back to the most-recent active child under the parent.
- Orphaned active Task entries whose `SubagentStop` never arrived are pruned after 24 hours when hook state is next read or a new session starts. This prevents a crashed or killed host process from leaking stale child state into later captures.

## Fallback for Missing Capabilities

| Missing Capability | Fallback |
|---|---|
| Compact hooks | MCP `get_context` / `session_handoff` on demand |
| Failure event | Parse exit code from tool_response in audit script |
| Agent type | Use client name only (e.g., `codex`, `gemini`) |

## 2026 Q2 host capability notes

Traceary's managed hook set intentionally lags the newest host-specific
surfaces so the contract stays stable across multiple releases. The table
below records capabilities available as of 2026 Q2 that are **not** wired
into the default install. `traceary doctor` surfaces the same notes at
runtime under the `<client>-host-capabilities` check.

| Host | New capability | Status | Traceary behaviour |
|---|---|---|---|
| Claude Code | `SubagentStop` (available since 2026-01) | wired | persisted as `session_ended` with `[phase:subagent]` body prefix via `traceary hook subagent-stop claude` |
| Claude Code | `PreCompact` (available since 2026-01) | wired | persisted as `compact_summary` with `[phase:pre-compact]` body prefix via `traceary hook compact claude pre-compact`; `loadCompactSummary` filters the marker so handoff / memory_pack keep returning the latest post-compact digest |
| Codex CLI | `Stop.last_assistant_message` | wired | persisted as `transcript` event via `traceary hook transcript codex` alongside the existing session-stop hook on the Codex `Stop` event |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | opt-in per install | import path `traceary memory import codex` works regardless of the flag — the flag only changes Codex's own capture behaviour |
| Gemini CLI | `AfterAgent.prompt_response` | wired | persisted as `transcript` event via `traceary hook transcript gemini` on the Gemini `AfterAgent` event (Gemini has no Stop event) |
| Gemini CLI 0.38.x | Memory manager agent / auto-memory | preview | Traceary's Tier 3 surface does not yet subscribe to the preview signals |

Operators who want to enable any of the preview features above should
follow the upstream docs for their client. Traceary continues to record
the stable capability set documented in the Tier 1-3 tables above, and
the `doctor` informational checks are there to make the gap obvious
rather than to enforce enablement.
