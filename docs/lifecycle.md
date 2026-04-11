# Event Lifecycle

[日本語](./lifecycle.ja.md)

This document describes how Traceary records events across different AI agent clients.

## Client Lifecycles

### Claude Code (Tier 1: Full)

```
SessionStart → [UserPromptSubmit → PostToolUse]* → (PreCompact → PostCompact →)* → SessionEnd
```

| Hook Event | Matcher | Traceary Event Kind | Description |
|---|---|---|---|
| SessionStart | `*` | `session_started` | Session start with workspace resolution |
| SessionStart | `compact` | — | Inject compact-summary into new context (stdout) |
| UserPromptSubmit | `*` | `prompt` | User instruction text |
| PostToolUse | `Bash` | `command_executed` | Shell command with input/output/exit code |
| PostToolUse | `mcp__.*` | `command_executed` | MCP tool invocation |
| PostToolUseFailure | `Bash`, `mcp__.*` | `command_executed` | Failed tool execution (filterable via `failures_only`) |
| PostCompact | `*` | `compact_summary` | Structured summary on context compression |
| SessionEnd | `*` | `session_ended` | Session end |

### Codex CLI (Tier 2: Partial)

```
SessionStart → [PostToolUse]* → Stop
```

| Hook Event | Traceary Event Kind | Description |
|---|---|---|
| SessionStart | `session_started` | Session start |
| PostToolUse | `command_executed` | Tool execution |
| Stop | `session_ended` | Session end (uses Stop instead of SessionEnd) |

**Limitations**: No `compact` hooks, no `prompt` recording, no failure-specific events.

### Gemini CLI (Tier 3: Basic)

```
SessionStart → [AfterTool]* → SessionEnd
```

| Hook Event | Traceary Event Kind | Description |
|---|---|---|
| SessionStart | `session_started` | Session start |
| AfterTool | `command_executed` | Tool execution |
| SessionEnd | `session_ended` | Session end |

**Limitations**: No `compact` hooks, no `prompt` recording, no failure-specific events.

## Event Kinds

| Kind | Description | Source |
|------|-------------|--------|
| `note` | Free-text log entry | CLI `traceary log` / MCP `add_log` |
| `command_executed` | Command or tool execution record | PostToolUse hooks |
| `reviewed` | Review result | CLI / MCP |
| `session_started` | Session start boundary | SessionStart hooks |
| `session_ended` | Session end boundary | SessionEnd / Stop hooks |
| `compact_summary` | Structured summary from context compression | PostCompact hook |
| `prompt` | User instruction text | UserPromptSubmit hook |

## Data Flow

```
AI Client (Claude Code / Codex CLI / Gemini CLI)
  │
  ├─ Hook / Extension event
  │    │
  │    ▼
  │  traceary-*.sh (bash scripts in ~/.config/traceary/hook-scripts/)
  │    │
  │    ▼
  │  traceary CLI (log / audit / session start|end)
  │    │
  │    ▼
  │  SQLite (~/.config/traceary/traceary.db)
  │
  └─ MCP server (stdio transport)
       │
       ▼
     traceary mcp-server → SQLite
```

## Hook Script Mapping

| Script | Purpose | Clients |
|--------|---------|---------|
| `traceary-session.sh` | Session start/end | All |
| `traceary-audit.sh` | Command/tool audit | All |
| `traceary-compact.sh` | Compact summary recording | Claude Code |
| `traceary-prompt.sh` | User prompt recording | Claude Code |
| `common.sh` | Shared helper functions | All |
