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
| PostToolUse | `Bash` | `command_executed` | Shell command with input/output |
| PostToolUse | `mcp__.*` | `command_executed` | MCP tool invocation |
| PostToolUse | built-in tools | `command_executed` | File I/O / search / agent / web / plan-mode exit (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`). Added in v0.8-6; expanded in v0.8-6b. |
| PostToolUseFailure | `Bash`, `mcp__.*`, built-in tools | `command_executed` | Failed tool execution (filterable via `failures_only`) |
| PostCompact | `*` | `compact_summary` | Structured summary on context compression |
| Stop | `*` | `transcript` | Last assistant text blocks (reasoning) from the stop-hook `transcript_path` |
| SessionEnd | `*` | `session_ended` | Session end |

### Codex CLI (Tier 2: Partial)

```
SessionStart → [UserPromptSubmit → PostToolUse → Stop]*
```

| Hook Event | Traceary Event Kind | Description |
|---|---|---|
| SessionStart | `session_started` | Session start |
| UserPromptSubmit | `prompt` | User instruction text |
| PostToolUse | `command_executed` | Tool execution |
| Stop | `transcript` | Final assistant message of each turn; a turn boundary, not a session end (#1170) |

**Limitations**: No host-level session-end signal — Codex fires `Stop` after every assistant response, so a Codex session stays open until an explicit end (MCP `manage_session`) or stale GC (`traceary session gc`). No `compact` hooks, no failure-specific events.

### Gemini CLI (Tier 3: Basic) — *legacy compatibility*

```
SessionStart → [AfterTool]* → SessionEnd
```

| Hook Event | Traceary Event Kind | Description |
|---|---|---|
| SessionStart | `session_started` | Session start |
| BeforeAgent | `prompt` | User instruction text (`prompt` field) |
| AfterAgent | `transcript` | Final agent response text (`prompt_response` field) |
| AfterTool | `command_executed` | Tool execution |
| PreCompress | `compact_summary` | Pre-compact marker only (`trigger` field; Gemini has no post-compress event) |
| SessionEnd | `session_ended` | Session end |

**Limitations**: No post-compress digest (Gemini fires `PreCompress` async only), no failure-specific events.

### Antigravity (Tier 2: Partial)

```
[PreInvocation → PreToolUse → PostToolUse → Stop]*
```

| Hook Event | Traceary Event Kind | Description |
|---|---|---|
| PreInvocation | `session_started` | Idempotent session start/refresh keyed by `conversationId` (Antigravity has no `SessionStart`) |
| PreToolUse (`run_command`) | — | Persists the proposed `{CommandLine, Cwd}` keyed by `conversationId + stepIdx`; never blocks |
| PostToolUse (`run_command`) | `command_executed` | Pairs the command from `PreToolUse` for the same step and records the audit (with step `error`) |
| Stop | `transcript` | Turn transcript from `transcriptPath` plus a turn boundary when the host emits `Stop`; does not close the session (#1170) |

**Limitations**: No `SessionStart` (first signal is `PreInvocation`) and no host session-end signal — like Codex, `Stop` is a per-execution turn boundary, so an Antigravity session stays open until an explicit end (MCP `manage_session`) or stale GC (`traceary session gc`). Only `run_command` tool calls are audited; prompt/transcript extraction from `transcriptPath` is best effort. Current interactive and headless `agy --print` runs emit Stop; `antigravity-event-coverage` checks recent database evidence for runtime gaps. See the [capture matrix](./integrations/antigravity.md).

> **v0.21 note**: Gemini CLI is the legacy compatibility path. The successor host, Antigravity, became a supported Traceary hook client in v0.21.1 (capability diagnostics only in v0.21.0). See [Antigravity integration status](./integrations/antigravity.md).

## Event Kinds

| Kind | Description | Source |
|------|-------------|--------|
| `note` | Free-text log entry | CLI `traceary log` / MCP `record_event(type="log")` |
| `command_executed` | Command or tool execution record | PostToolUse hooks |
| `reviewed` | Review result | CLI / MCP |
| `session_started` | Session start boundary | SessionStart hooks (Claude / Codex / Gemini); PreInvocation (Antigravity) |
| `session_ended` | Session end boundary | SessionEnd hooks (Claude / Gemini); Codex and Antigravity have no host session-end signal (#1170) |
| `compact_summary` | Structured summary from context compression | PostCompact hook |
| `prompt` | User instruction text | UserPromptSubmit (Claude / Codex), BeforeAgent (Gemini) hooks |
| `transcript` | Last assistant-message text blocks (reasoning / explanation). Tool-use blocks are excluded — those are captured by `command_executed`. | Stop hook (Claude Code / Codex / Antigravity), AfterAgent (Gemini) |

## Data Flow

```
AI Client (Claude Code / Codex CLI / Gemini CLI)
  │
  ├─ Hook / Extension event
  │    │
  │    ▼
  │  traceary hook ... (hidden Go runtime entrypoints)
  │    │
  │    ├─ packaged shell wrappers (compatibility only, when present)
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
| `traceary hook session <client> <start|end|stop>` | Session start/end | All |
| `traceary hook audit <client>` | Command/tool audit | All |
| `traceary hook compact <client> <post-compact|session-start-compact>` | Compact summary recording / compact resume output | Claude Code |
| `traceary hook prompt <client>` | User prompt recording | Claude Code, Codex CLI, Gemini CLI |
| `traceary hook transcript <client>` | Assistant-message transcript recording (Stop hook for Claude / Codex / Antigravity, AfterAgent for Gemini) | Claude Code, Codex CLI, Gemini CLI, Antigravity |
| packaged shell wrappers under `scripts/hooks/` | Compatibility layer that forwards into `traceary hook ...` | Packaged integrations / legacy installs |
