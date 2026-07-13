# Lifecycle Events

[日本語](./lifecycle-events.ja.md)

This page documents Traceary's **canonical lifecycle event kinds** — the six `EventKind` values that are normally emitted by hooks and form the audit (L1) timeline of a session.

The full enum lives in [`domain/types/event_kind.go`](../../domain/types/event_kind.go). The two non-lifecycle kinds (`note`, `reviewed`) are operator-driven and not covered here. For the per-client hook → event mapping see [Event Lifecycle](../lifecycle.md); for the hook capability tiers see [Hook Contract](./contract.md).

## At a glance

| Kind | When emitted | Typical source hook | Body |
|---|---|---|---|
| `session_started` | A new agent session opens | `SessionStart` (Claude / Codex / Gemini *(legacy)*) | workspace + agent identifier (free-text) |
| `prompt` | The user submits an instruction | `UserPromptSubmit` (Claude / Codex) | raw prompt text (redacted) |
| `command_executed` | A tool / shell call completes (success or failure) | `PostToolUse`, `PostToolUseFailure`, `AfterTool` *(Gemini legacy)* | input / output / structural failure flag (compact JSON, redacted) |
| `transcript` | Assistant turn ends with reasoning / explanation text | `Stop` (Claude / Codex) | last assistant-message text blocks (redacted) |
| `compact_summary` | Host context compression produces a summary | `PostCompact` (Claude only today) | structured compact summary text |
| `session_ended` | The agent session closes | `SessionEnd` (Claude / Gemini *(legacy)*); Codex has no host session-end signal (#1170) | optional reason marker |

All event bodies pass through built-in secret redaction plus operator-configured `redact.rules` / `redact.extra_patterns` before being persisted.

## Per-event detail

### `session_started`

- Marks the open boundary of a session row in `sessions`.
- Resolved session ID order: hook payload `session_id` → cached state in `TRACEARY_HOOK_STATE_DIR` → freshly generated UUID via `traceary session start`.
- Workspace resolution prefers normalized `remote.origin.url`, then the local git worktree root, then the raw hook `cwd`.
- Used by L2 / L3 (handoff, context pack) to scope queries to a single session.

### `prompt`

- Captures the user's instruction text verbatim (after redaction). Visible in `traceary timeline`, `traceary search`, and the L2 `get_context` body.
- Claude Code (`UserPromptSubmit`), Codex CLI (`UserPromptSubmit`), and Gemini CLI (`BeforeAgent`) all emit this — see [host-coverage.md](./host-coverage.md).
- Body marker: none (raw text). Distinct from `transcript`, which is the assistant side.

### `command_executed`

- Emitted for every tool invocation a host runs through Traceary's audit hook.
- Body shape (compact JSON):
  - `command`: `tool_input.command` when present (Bash etc.)
  - `input`: compact JSON of `tool_input`
  - `output`: compact JSON of `tool_response`, or `{error, is_interrupt}` for failure payloads
- Failure detection is structural, not exit-code based: no host puts a numeric exit code in the post-tool payload, so Traceary marks an audit `failed` from the failure-shaped payload — Claude's `PostToolUseFailure` (top-level `error`) and Gemini's `tool_response.error` (spawn errors only). `failures_only` in `traceary list events` matches that flag. Codex exposes no structured failure signal, so its failed runs are recorded as unflagged audits.
- This is the highest-volume event kind in a typical session — most search / timeline queries touch it.

### `transcript`

- Captures the assistant's reasoning / explanation text blocks at the end of a response turn (Claude `Stop`, Codex `Stop`'s `last_assistant_message`).
- Tool-use blocks are excluded; those are already captured as `command_executed`.
- Provides the "what did the agent decide" half of the timeline so L2 summaries can be reconstructed without replaying tool I/O.

### `compact_summary`

- Emitted when the host compresses its in-memory context window.
- Claude Code (`PostCompact`) emits a digest body. Gemini CLI (`PreCompress`) records a marker only because Gemini exposes no post-compress event with the resulting summary. Codex CLI 0.144.1 exposes `PreCompact` and `PostCompact`, but their payload contains only `trigger`; Traceary records phase-specific markers rather than claiming a compacted summary body.
- Used by L2 to seed a `sessions.summary` value when a session is later resumed via `SessionStart` matcher `compact`.

### `session_ended`

- Marks the close boundary of a session row.
- Claude / Gemini use a dedicated `SessionEnd` hook. Codex exposes no `SessionEnd` and its `Stop` fires after every assistant response (a turn boundary, not a session end), so a Codex session ends only via an explicit signal (MCP `manage_session`) or stale GC (`traceary session gc`) — see [host-coverage.md](./host-coverage.md) and #1170.
- Best-effort: hosts may exit without firing the hook (kill -9, crashed shell). L2 reconciliation tolerates dangling sessions, and stale GC closes long-idle open sessions.

## Antigravity (v0.21.1+)

Antigravity is the successor to Gemini CLI as a Traceary-integrated host. As of v0.21.1 it is a supported hook client (capability diagnostics only in v0.21.0). It maps onto the canonical event kinds like a Tier 2 host:

- `session_started` — from `PreInvocation` (Antigravity has no `SessionStart`), idempotent and keyed by `conversationId`.
- `command_executed` — from `PostToolUse` (`run_command` only), paired with the args persisted by the preceding `PreToolUse` for the same `stepIdx`.
- `transcript` — from `Stop`, read best-effort from `transcriptPath`; `Stop` is a per-execution turn boundary, not a session end (#1170), so it emits no `session_ended`. `Stop` fires on interactive runs only — headless `agy --print` emits no `Stop` (or any finalization hook), so a print run records session start + `run_command` audit only, with no `transcript` event or turn boundary. See the [capture matrix](../integrations/antigravity.md) and the `antigravity-capture-levels` doctor check.

There is no `prompt`, `compact_summary`, or `session_ended` event from Antigravity. Gemini CLI hook coverage above reflects the legacy compatibility path.

See [Antigravity hooks and plugin](../integrations/antigravity.md) for the full contract and limitations.

## Where to go next

- [Event Lifecycle](../lifecycle.md) — hook → event mapping per client.
- [Hook Contract](./contract.md) — capability tiers (Tier 1 / 2 / 3).
- [Host hook coverage matrix](./host-coverage.md) — wired vs available vs unsupported per host.
- [Memory layers](../memory/README.md) — how these events feed L1 / L2 / L3.
