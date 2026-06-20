# Hook Contract

[日本語](./contract.ja.md)

This document defines the hook capability tiers across AI agent clients.

## Capability Tiers

### Tier 1: Full (Claude Code)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | `*` | Record session start |
| SessionEnd | `*` | Record session end |
| PostToolUse | `Bash` | Record shell command audit |
| PostToolUse | `mcp__.*` | Record MCP tool audit |
| PostToolUse | `Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode` | Record built-in Claude Code tool invocations (file I/O, search, agent, web, plan-mode exit). Added in v0.8-6; expanded to include `NotebookRead` and `ExitPlanMode` in v0.8-6b |
| PostToolUseFailure | `Bash`, `mcp__.*`, built-in tools | Record a failure-flagged tool audit. The payload carries a top-level `error` string (no `tool_response`, no numeric exit code), so Traceary marks the audit `failed` rather than reading an exit code; `list --failures` matches the flag |
| PostCompact | `*` | Record compact summary |
| UserPromptSubmit | `*` | Record user prompt text |
| Stop | `*` | Record last assistant message from `transcript_path` as a `transcript` event (built-in secret redaction + operator-configured `redact.rules` / `redact.extra_patterns` applied) |
| SessionStart (compact) (planned) | `compact` | Inject context pointer via stdout |

### Tier 2: Partial (Codex)

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | (all) | Record session start |
| UserPromptSubmit | (all) | Record user prompt text |
| Stop | (all) | Record the final assistant message (from `last_assistant_message`) as a `transcript` event (built-in secret redaction + operator-configured `redact.rules` / `redact.extra_patterns` applied); treated as a turn boundary, not a session end |
| PostToolUse | (all) | Record tool audit |

**Limitations**: No SessionEnd and no host-level session-end signal — Codex fires `Stop` after every assistant response, not when the conversation ends, so Traceary treats `Stop` as a turn boundary and keeps the session open (#1170). A Codex session ends only via an explicit end signal (MCP `manage_session`) or stale GC (`traceary session gc`, default 24h). No compact hooks, no failure-specific event and no structured failure signal — Codex fires `PostToolUse` for non-zero exits too, but its `tool_response` is a plain formatted string with no exit code or error field, so failed runs are recorded as ordinary (unflagged) audits.

### Tier 3: Basic (Gemini CLI) — *legacy compatibility*

| Hook Event | Matcher | Behavior |
|---|---|---|
| SessionStart | `*` | Record session start |
| SessionEnd | `*` | Record session end |
| BeforeAgent | `*` | Record user prompt text (from `prompt`) as a `prompt` event |
| AfterAgent | `*` | Record the agent response (from `prompt_response`) as a `transcript` event (built-in secret redaction + operator-configured `redact.rules` / `redact.extra_patterns` applied) |
| AfterTool | `*` | Record tool audit |
| PreCompress | `*` | Record a `compact_summary` marker (`trigger` field only — Gemini exposes no post-compress digest) |

**Limitations**: No post-compress digest — Gemini's `PreCompress` is advisory-only and fires asynchronously before compression, and the gemini-cli 0.43.0 bundled hook reference exposes no post-compress event anywhere in its hook surface (`BeforeTool` / `AfterTool` / `BeforeAgent` / `AfterAgent` / `BeforeModel` / `BeforeToolSelection` / `AfterModel` / `SessionStart` / `SessionEnd` / `Notification` / `PreCompress`). No failure-specific event, no PostCompact/SessionStart(compact). Gemini has no Stop event, so transcript capture is attached to `AfterAgent` instead. Failure capture is partial: `AfterTool` exposes a nested `tool_response.error` object only for spawn/OS-level errors (Traceary marks those `failed`); a plain non-zero shell exit surfaces only as `Exit Code: N` text inside `tool_response.llmContent`, which Traceary deliberately does not parse, so those runs stay unflagged.

> **v0.21 note**: The successor host, Antigravity, is supported as a hook client from v0.21.1 (`PreInvocation` session start/refresh, `PreToolUse`/`PostToolUse` `run_command` pairing, and `Stop` transcript/turn boundary when the host emits it) against the documented public hook/plugin surface. Headless `agy --print` does not emit `Stop`, so its final turn is unavailable. The Gemini CLI details above are legacy compatibility and are preserved for existing installs. See [Antigravity hooks and plugin](../integrations/antigravity.md).

## Shared Behavior

All tiers:
- Session start persists the resolved workspace to the state file; audit reads the workspace from state
- Agent type resolution: `agent_type` field → hierarchical agent name (Claude only)
- Exit code extraction from `tool_response.exitCode` when a host provides it. In practice none of the current hosts populate this field in the post-tool payload, so failure is detected structurally instead (see the failure-flag rows above and the fallback table below) rather than from an exit code.
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
| Failure event | Derive a structural `failed` flag from the failure-shaped payload (Claude's top-level `error`, Gemini's `tool_response.error`); `list --failures` matches `failed = 1` in addition to a non-zero `exit_code`. Hosts that expose no structured failure signal (Codex; Gemini plain non-zero exits) are recorded as unflagged audits |
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
| Codex CLI | `Stop.last_assistant_message` | wired | persisted as `transcript` event via `traceary hook transcript codex` on the Codex `Stop` event; the paired `hook session codex stop` runs as a turn boundary (keeps the session open) rather than a session end (#1170) |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | opt-in per install | import path `traceary memory admin import codex` works regardless of the flag — the flag only changes Codex's own capture behaviour |
| Gemini CLI | `AfterAgent.prompt_response` | wired | persisted as `transcript` event via `traceary hook transcript gemini` on the Gemini `AfterAgent` event (Gemini has no Stop event) |
| Gemini CLI | `BeforeAgent.prompt` | wired | persisted as `prompt` event via `traceary hook prompt gemini` on the Gemini `BeforeAgent` event (parity with Claude / Codex `UserPromptSubmit`) |
| Gemini CLI | `PreCompress.trigger` | wired (marker only) | persisted as `compact_summary` event with `source_hook=pre_compact` and the `trigger` value as body via `traceary hook compact gemini pre-compact` (Gemini exposes no post-compress digest) |
| Gemini CLI | Memory manager agent / auto-memory | experimental (re-verified on 0.43.0) | Traceary's Tier 3 surface does not yet subscribe to the experimental signals |

Operators who want to enable any of the preview features above should
follow the upstream docs for their client. Traceary continues to record
the stable capability set documented in the Tier 1-3 tables above, and
the `doctor` informational checks are there to make the gap obvious
rather than to enforce enablement.
