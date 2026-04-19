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
| UserPromptSubmit | (all) | Record user prompt text |
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
- Session start persists the resolved workspace to the state file; audit reads the workspace from state
- Agent type resolution: `agent_type` field → hierarchical agent name (Claude only)
- Exit code extraction from `tool_response.exitCode` when available
- MCP tool name fallback: `tool_input.command` → `tool_name`

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
| Claude Code | `SubagentStop` (available since 2026-01) | available | subagent lineage still resolved from `agent_type` on `PostToolUse`; no dedicated hook |
| Claude Code | `PreCompact` (available since 2026-01) | available | compact capture still routes through `PostCompact`; pre-compact snapshotting is not wired |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | opt-in per install | import path `traceary memory import codex` works regardless of the flag — the flag only changes Codex's own capture behaviour |
| Gemini CLI 0.38.x | Memory manager agent / auto-memory | preview | Traceary's Tier 3 surface does not yet subscribe to the preview signals |

Operators who want to enable any of the preview features above should
follow the upstream docs for their client. Traceary continues to record
the stable capability set documented in the Tier 1-3 tables above, and
the `doctor` informational checks are there to make the gap obvious
rather than to enforce enablement.
