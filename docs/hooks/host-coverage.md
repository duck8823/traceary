# Host Hook Coverage

[日本語](./host-coverage.ja.md)

This page tracks, per host AI agent, which Traceary [lifecycle events](./lifecycle-events.md) are wired to a real hook today, which hooks the host exposes but Traceary does not yet wire, and which are not supported by the host at all.

Legend:

- `●` wired in the packaged Traceary integration today
- `○` available in the host but not wired by Traceary yet
- `✕` not exposed by this host

### Status semantics

The statuses describe **Traceary's wiring state**, not the host's capability:

- **wired** — the packaged Traceary integration captures this lifecycle event today. By convention, wired cells are backed by a live-verified host contract (payload probed, fixtures committed), though the matrix itself only asserts the wiring. Wired events are the ones you can expect in the Traceary DB for that host; see `traceary list events --kind <event>` in the Verification column.
- **available** — the host exposes a hook or signal for this event, but Traceary does not wire it (yet). This is **not** a capture claim: the event will not appear in the Traceary DB for that host. Examples: Grok `SessionEnd` (documented but not emitted in probes), Kimi `PreCompact`/`PostCompact` before v0.29.0.
- **unsupported** — the host does not expose a usable signal for this event (e.g. Codex/Antigravity session end; they end via MCP `manage_session` or stale GC instead).

Relationship to the machine-readable [host contract](./host-contract.json): contract events classified `supported` / `best_effort` with live fixtures back the **wired** cells; events the host documents or emits but Traceary does not wire (contract `unavailable` with a host-side signal, e.g. Grok `SessionEnd`) back the **available** cells; events with no usable host signal back the **unsupported** cells. The matrix itself lives in `application/hostcoverage/matrix.json` and this table is generated from it — do not edit the generated block below.

**Last verified: 2026-07-19 (Kimi Code 0.27.0 live hook probe and integration (#1393); Grok Build 0.2.101 re-probe for unobserved hooks + 0.2.99 fixtures; Antigravity CLI 1.1.1 and the current official hook contract; Gemini CLI re-verified 2026-06-10 against 0.43.0).** Refresh this page when bumping Traceary integration packages or when a host CLI release changes its hook surface.

> **v0.21.1 note:** Gemini CLI hook coverage in this matrix is **legacy compatibility only**. Gemini CLI is the legacy Google AI agent host; Antigravity (`/Applications/Antigravity.app`) is the active successor. As of **v0.21.1, Antigravity is a supported hook client** with a packaged plugin against the documented public hook surface (`integrations/antigravity-plugin/`). See the [Antigravity hooks and plugin guide](../integrations/antigravity.md).

## Lifecycle event → host hook matrix

<!-- host-coverage-matrix:begin -->
<!-- DO NOT EDIT: generated from application/hostcoverage/matrix.json via `go run ./cmd/repo-tooling docs generate-host-coverage`. -->
| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.144.1 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Antigravity (`antigravity-plugin`) | Kimi Code 0.27.0 (`kimi-plugin`) | Grok Build 0.2.99 | Verification |
|---|---|---|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | ● `PreInvocation` (idempotent, keyed by `conversationId`; Antigravity has no `SessionStart`) | ● `SessionStart` (`source` = startup|resume; resume re-fires with the same session_id, recorded idempotently) | ● `SessionStart` (native `agent=grok`) | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | ● `Stop` (no direct prompt field; recovered from the latest `USER_INPUT` / `USER_EXPLICIT` row at `transcriptPath`) | ● `UserPromptSubmit` (`prompt` content-block array flattened to text) | ● `UserPromptSubmit` (`prompt`; native `agent=grok`) | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | ● `PreToolUse` + `PostToolUse` (`run_command`; command args paired across the two events by `conversationId + stepIdx`) | ● `PostToolUse` (`tool_output` string) + `PostToolUseFailure` (`error` object flattened; `PreToolUse` is validation-only) | ● `PreToolUse` validation + `PostToolUse` completed audit (input/result are co-located; missing/denied reads are failed result variants) | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | ● `Stop` (`transcriptPath`, best-effort lenient JSONL scan) | ● `Stop` (best-effort: `session_index.jsonl` → session `wire.jsonl` last-turn `content.part` think/text blocks) | ● `Stop` (best-effort current-prompt message chunks from `updates.jsonl`; no model field) | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` resume) | ● `PreCompact` + `PostCompact` markers (`trigger` only; Codex exposes no compacted summary body) | ● `PreCompress` (marker only — Gemini exposes no post-compress hook with the resulting summary) | ✕ no documented compact hook | ● `PreCompact` + `PostCompact` (recorded as `trigger` markers — auto observed live, manual not yet probed; payload token counts are not persisted) | ● `PreCompact` + `PostCompact` (native live-observed markers from `source`; no summary body) | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ no host session-end signal — Codex `Stop` is a per-response turn boundary, not a session end (#1170); ends via MCP `manage_session` or stale GC | ● `SessionEnd` | ✕ no host session-end signal — Antigravity `Stop` is a per-execution boundary, not a session end (#1170); ends via MCP `manage_session` or stale GC | ● `SessionEnd` (`reason` = exit) | ○ documented `SessionEnd`, but not emitted by headless completion, TUI `/quit`, or TUI `/new` probes | `traceary list events --kind session_ended --limit 5` |
<!-- host-coverage-matrix:end -->

> **Antigravity headless `agy --print`:** the current CLI emits `PreInvocation`, `PreToolUse`/`PostToolUse` when needed, and `Stop` with `transcriptPath`. Traceary recovers prompt and transcript at Stop. `antigravity-event-coverage` detects runtime gaps from database evidence. Hook events are stored with `client=hook`, `agent=antigravity`, so verify them with `traceary list --agent antigravity`.

> **Grok Build contract (fixtures 0.2.99; re-probed 0.2.101 on 2026-07-16):** `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `PreCompact`, and `PostCompact` were captured live in sanitized empty workspaces. Re-probe on 0.2.101 still did not emit standalone `PostToolUseFailure`, `PermissionDenied`, or `SessionEnd`; a missing-file Read returned `FileNotFound` nested under `PostToolUse`. Spawning a subagent used the `spawn_subagent` tool and produced tool audits only — no `SubagentStart`/`SubagentStop` hook payloads and no parent/child identity contract. Traceary does not synthesize those unobserved hooks. Field-level evidence is in [`host-contract.json`](./host-contract.json).

> **Grok native runtime:** `traceary hooks install --client grok` writes `.grok/hooks/traceary.json` (or `~/.grok/hooks/traceary.json` with `--global`). Core and compact events are stored with `client=hook`, `agent=grok`. `Stop` remains a turn boundary. Subagent capture remains unavailable (no dedicated parent/child hook payload observed on 0.2.101).

### Other host hooks Traceary does not wire today

This list excludes hooks that already appear in the lifecycle matrix above.

| Host | Hook | Status | Note |
|---|---|---|---|
| Claude Code | `SubagentStart` (`PreToolUse matcher=Task\|Agent`) | ● wired (subagent capture, not a lifecycle event) | recorded as `note` body marker, not in the six lifecycle kinds |
| Claude Code | `SubagentStop` | ● wired (subagent capture) | same |
| Claude Code | `Notification`, `PreToolUse` (other matchers), `StopFailure`, `UserPromptExpansion`, `PermissionRequest`, `PermissionDenied`, `PostToolBatch`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `Elicitation`, `ElicitationResult` | ○ available | not in current packaged hooks |
| Codex CLI | `SubagentStart`, `SubagentStop` | ● wired (child-session capture) | correlated by `agent_id`; `agent_type` names the child agent |
| Codex CLI | `PreToolUse`, `PermissionRequest` | ○ available | not wired |
| Gemini CLI | `BeforeTool`, `BeforeToolSelection`, `BeforeModel`, `AfterModel`, `Notification` | ○ available | not wired |
| Antigravity | `PreToolUse` for non-`run_command` tools | ○ available | only `run_command` is audited |
| Grok Build | `PostToolUseFailure`, `PermissionDenied`, `SessionEnd`, `StopFailure` | ○ documented, not live-confirmed on 0.2.99 or re-probe 0.2.101 | unavailable to Traceary until a live payload is observed; missing-file and tool denial currently arrive through `PostToolUse` |
| Grok Build | `SubagentStart`, `SubagentStop` | ○ documented, not live-emitted on 0.2.101 | unavailable; spawn uses `spawn_subagent` tool audits only — no parent/child hook payload (#1299) |
| Grok Build | `Notification` | ○ documented, not probed | no Traceary lifecycle mapping; unavailable |

## Per-host references

- Claude Code: https://code.claude.com/docs/en/hooks · packaged config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: official Codex CLI 0.144.1 hook reference (`SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`). Packaged config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: hooks reference shipped with the local install at `/opt/homebrew/Cellar/gemini-cli/0.43.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md` (no post-compress event in the documented hook surface; `PreCompress` is advisory-only and fires asynchronously before compression). Packaged config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)
- Antigravity: public hook surface documented at https://www.antigravity.google/docs/hooks and https://antigravity.google/assets/docs/editor/ide-hooks.md; plugin packaging at https://antigravity.google/assets/docs/cli/cli-plugins.md (verified 2026-06-20 JST). Packaged config: [`integrations/antigravity-plugin/hooks.json`](../../integrations/antigravity-plugin/hooks.json)
- Grok Build: official hook surface at https://docs.x.ai/build/features/hooks (last updated 2026-07-02); live 0.2.99 payload contract: [`host-contract.json`](./host-contract.json); sanitized fixtures: [`presentation/cli/testdata/grok_hooks/v0.2.99`](../../presentation/cli/testdata/grok_hooks/v0.2.99/)

## Maintenance

The lifecycle matrix table above is generated from the machine-readable source
[`application/hostcoverage/matrix.json`](../../application/hostcoverage/matrix.json).
Doctor host-capability and event-coverage expectations load the same embedded matrix.

To refresh:

1. Bump or re-install the host CLI you want to re-check.
2. Update `application/hostcoverage/matrix.json` (status, bilingual summaries, `last_verified`).
3. Run `go run ./cmd/repo-tooling docs generate-host-coverage` to rewrite the marked table sections.
4. For each `●` cell, run the verification command and confirm a recent row exists in `~/.config/traceary/traceary.db`.
5. Run `go run ./cmd/repo-tooling docs verify-host-coverage` (also enforced in CI).

A daily drift check is wired through `/schedule` (see #814).
