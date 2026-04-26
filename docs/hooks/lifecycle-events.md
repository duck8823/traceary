# Lifecycle Events

[日本語](./lifecycle-events.ja.md)

This page documents Traceary's **canonical lifecycle event kinds** — the six `EventKind` values that are normally emitted by hooks and form the audit (L1) timeline of a session.

The full enum lives in [`domain/types/event_kind.go`](../../domain/types/event_kind.go). The two non-lifecycle kinds (`note`, `reviewed`) are operator-driven and not covered here. For the per-client hook → event mapping see [Event Lifecycle](../lifecycle.md); for the hook capability tiers see [Hook Contract](./contract.md).

## At a glance

| Kind | When emitted | Typical source hook | Body |
|---|---|---|---|
| `session_started` | A new agent session opens | `SessionStart` (Claude / Codex / Gemini) | workspace + agent identifier (free-text) |
| `prompt` | The user submits an instruction | `UserPromptSubmit` (Claude / Codex) | raw prompt text (redacted) |
| `command_executed` | A tool / shell call completes (success or failure) | `PostToolUse`, `PostToolUseFailure`, `AfterTool` | input / output / exit code (compact JSON, redacted) |
| `transcript` | Assistant turn ends with reasoning / explanation text | `Stop` (Claude / Codex) | last assistant-message text blocks (redacted) |
| `compact_summary` | Host context compression produces a summary | `PostCompact` (Claude only today) | structured compact summary text |
| `session_ended` | The agent session closes | `SessionEnd` (Claude / Gemini) or `Stop` (Codex) | optional reason marker |

All event bodies pass through built-in secret redaction plus operator-configured `redact.rules` / `redact.extra_patterns` before being persisted.

## Per-event detail

### `session_started`

- Marks the open boundary of a session row in `sessions`.
- Resolved session ID order: hook payload `session_id` → cached state in `TRACEARY_HOOK_STATE_DIR` → freshly generated UUID via `traceary session start`.
- Workspace resolution prefers normalized `remote.origin.url`, then the local git worktree root, then the raw hook `cwd`.
- Used by L2 / L3 (handoff, context pack) to scope queries to a single session.

### `prompt`

- Captures the user's instruction text verbatim (after redaction). Visible in `traceary timeline`, `traceary search`, and the L2 `get_context` body.
- Today only Claude Code and Codex CLI emit this. Gemini CLI exposes `BeforeAgent` but Traceary does not wire it as a prompt source yet — see [host-coverage.md](./host-coverage.md) and #806.
- Body marker: none (raw text). Distinct from `transcript`, which is the assistant side.

### `command_executed`

- Emitted for every tool invocation a host runs through Traceary's audit hook.
- Body shape (compact JSON):
  - `command`: `tool_input.command` when present (Bash etc.)
  - `input`: compact JSON of `tool_input`
  - `output`: compact JSON of `tool_response`, or `{error, is_interrupt}` for failure payloads
- Failure payloads are routed via `PostToolUseFailure` (Claude) so they can be filtered with `failures_only` in `traceary list events`.
- This is the highest-volume event kind in a typical session — most search / timeline queries touch it.

### `transcript`

- Captures the assistant's reasoning / explanation text blocks at the end of a response turn (Claude `Stop`, Codex `Stop`'s `last_assistant_message`).
- Tool-use blocks are excluded; those are already captured as `command_executed`.
- Provides the "what did the agent decide" half of the timeline so L2 summaries can be reconstructed without replaying tool I/O.

### `compact_summary`

- Emitted when the host compresses its in-memory context window.
- Today only Claude Code emits this (`PostCompact`). Codex 0.125 has no compact hook (upstream `openai/codex#16098`). Gemini exposes `PreCompress` but not a post-compress event with the resulting summary — Traceary plans a `PreCompress` marker in #807.
- Used by L2 to seed a `sessions.summary` value when a session is later resumed via `SessionStart` matcher `compact`.

### `session_ended`

- Marks the close boundary of a session row.
- Claude / Gemini use a dedicated `SessionEnd` hook. Codex piggybacks on `Stop` because its CLI exposes no `SessionEnd` (see [host-coverage.md](./host-coverage.md)).
- Best-effort: hosts may exit without firing the hook (kill -9, crashed shell). L2 reconciliation tolerates dangling sessions.

## Where to go next

- [Event Lifecycle](../lifecycle.md) — hook → event mapping per client.
- [Hook Contract](./contract.md) — capability tiers (Tier 1 / 2 / 3).
- [Host hook coverage matrix](./host-coverage.md) — wired vs available vs unsupported per host.
- [Memory layers](../memory/README.md) — how these events feed L1 / L2 / L3.
