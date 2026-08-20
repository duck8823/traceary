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
- **unsupported** — the host does not expose a usable signal for this event (e.g. Codex/Antigravity session end; they end via `traceary session end` or stale GC instead).

Relationship to the machine-readable [host contract](./host-contract.json): contract events classified `supported` / `best_effort` with live fixtures back the **wired** cells; events the host documents or emits but Traceary does not wire (contract `unavailable` with a host-side signal, e.g. Grok `SessionEnd`) back the **available** cells; events with no usable host signal back the **unsupported** cells. The matrix itself lives in `application/hostcoverage/matrix.json` and this table is generated from it — do not edit the generated block below.

**Last verified: 2026-08-21 (Grok Build 1.0.5 headless `-p` and TUI probes on an isolated store: the traceary-grok plugin is discovered, enabled, and auto-trusted, but the host dispatched only global/user-level hooks — no plugin-provided hook ran and zero Traceary events were recorded, so Grok cells stay `available` until a host version dispatches plugin hooks again. Previously: Kimi Code 0.27.0 live hook probe and integration (#1393); Grok Build 0.2.101 re-probe for unobserved hooks + 0.2.99 fixtures; Antigravity CLI 1.1.1 and the current official hook contract; Gemini CLI re-verified 2026-06-10 against 0.43.0).** Refresh this page when bumping Traceary integration packages or when a host CLI release changes its hook surface.

> **v0.21.1 note:** Gemini CLI hook coverage in this matrix is **legacy compatibility only**. Gemini CLI is the legacy Google AI agent host; Antigravity (`/Applications/Antigravity.app`) is the active successor. As of **v0.21.1, Antigravity is a supported hook client** with a packaged plugin against the documented public hook surface (`integrations/antigravity-plugin/`). See the [Antigravity hooks and plugin guide](../integrations/antigravity.md).

## Lifecycle event → host hook matrix

<!-- host-coverage-matrix:begin -->
<!-- DO NOT EDIT: generated from application/hostcoverage/matrix.json via `go run ./cmd/repo-tooling docs generate-host-coverage`. -->
| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.145.0 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Antigravity (`antigravity-plugin`) | Kimi Code 0.27.0 (`kimi-plugin`) | Grok Build 1.0.5 | Verification |
|---|---|---|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | ● `PreInvocation` (idempotent, keyed by `conversationId`; Antigravity has no `SessionStart`) | ● `SessionStart` (`source` = startup|resume; resume re-fires with the same session_id, recorded idempotently) | ○ `SessionStart` is wired in the plugin, but Grok Build 1.0.5 dispatched no plugin-provided hooks in headless `-p` or TUI probes (2026-08-21); last live capture on 0.2.99/0.2.101 | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | ● `Stop` (no direct prompt field; recovered from the latest `USER_INPUT` / `USER_EXPLICIT` row at `transcriptPath`) | ● `UserPromptSubmit` (`prompt` content-block array flattened to text) | ○ `UserPromptSubmit` is wired in the plugin, but Grok Build 1.0.5 dispatched no plugin-provided hooks in headless `-p` or TUI probes (2026-08-21); last live capture on 0.2.99/0.2.101 | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | ● `PreToolUse` + `PostToolUse` (`run_command`; command args paired across the two events by `conversationId + stepIdx`) | ● `PostToolUse` (`tool_output` string) + `PostToolUseFailure` (`error` object flattened; `PreToolUse` is validation-only) | ○ `PreToolUse`/`PostToolUse` are wired in the plugin, but Grok Build 1.0.5 dispatched no plugin-provided hooks in headless `-p` or TUI probes (2026-08-21); last live capture on 0.2.99/0.2.101 | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | ● `Stop` (`transcriptPath`, best-effort lenient JSONL scan) | ● `Stop` (best-effort: `session_index.jsonl` → session `wire.jsonl` last-turn `content.part` think/text blocks) | ○ `Stop` is wired in the plugin, but Grok Build 1.0.5 dispatched no plugin-provided hooks in headless `-p` or TUI probes (2026-08-21); last live capture on 0.2.99/0.2.101 | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` resume) | ● `PreCompact` + `PostCompact` markers (`trigger` only; Codex exposes no compacted summary body) | ● `PreCompress` (marker only — Gemini exposes no post-compress hook with the resulting summary) | ✕ no documented compact hook | ● `PreCompact` + `PostCompact` (recorded as `trigger` markers — auto observed live, manual not yet probed; payload token counts are not persisted) | ○ `PreCompact`/`PostCompact` are wired in the plugin, but Grok Build 1.0.5 dispatched no plugin-provided hooks in headless `-p` or TUI probes (2026-08-21); last live capture on 0.2.99/0.2.101 | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ no host session-end signal — Codex `Stop` is a per-response turn boundary, not a session end (#1170); ends via `traceary session end` or stale GC | ● `SessionEnd` | ✕ no host session-end signal — Antigravity `Stop` is a per-execution boundary, not a session end (#1170); ends via `traceary session end` or stale GC | ● `SessionEnd` (`reason` = exit) | ○ documented `SessionEnd`; 1.0.5 probes dispatched it to user-level hooks at teardown, but plugin-provided hooks (Traceary's route) are not dispatched | `traceary list events --kind session_ended --limit 5` |
<!-- host-coverage-matrix:end -->

> **Antigravity headless `agy --print`:** the current CLI emits `PreInvocation`, `PreToolUse`/`PostToolUse` when needed, and `Stop` with `transcriptPath`. Traceary recovers prompt and transcript at Stop. `antigravity-event-coverage` detects runtime gaps from database evidence. Hook events are stored with `client=hook`, `agent=antigravity`, so verify them with `traceary list --agent antigravity`.

> **Codex headless `codex exec`:** local marker-only probes on Codex CLI 0.145.0 observed `SessionStart`, `UserPromptSubmit`, and `Stop` in both normal and `--ephemeral` runs, with text and `--json` output. `Stop.last_assistant_message` is the stable final-turn source in both modes; ephemeral payloads omit `transcript_path`. Traceary does not inspect another rollout to compensate for a missing `Stop`. `codex-capture` reports `final_turn_not_observed` until runtime evidence exists.

> **Grok Build contract (fixtures 0.2.99; re-probed 0.2.101 on 2026-07-16):** `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `PreCompact`, and `PostCompact` were captured live in sanitized empty workspaces. Re-probe on 0.2.101 still did not emit standalone `PostToolUseFailure`, `PermissionDenied`, or `SessionEnd`; a missing-file Read returned `FileNotFound` nested under `PostToolUse`. Spawning a subagent used the `spawn_subagent` tool and produced tool audits only — no `SubagentStart`/`SubagentStop` hook payloads and no parent/child identity contract. Traceary does not synthesize those unobserved hooks. Field-level evidence is in [`host-contract.json`](./host-contract.json).

> **Grok native runtime:** `traceary hooks install --client grok` writes `.grok/hooks/traceary.json` (or `~/.grok/hooks/traceary.json` with `--global`). Core and compact events are stored with `client=hook`, `agent=grok`. `Stop` remains a turn boundary. Subagent capture remains unavailable (no dedicated parent/child hook payload observed on 0.2.101).

> **Grok Build 1.0.5 probe (2026-08-21, isolated store):** a fresh headless `grok -p` run and a short TUI session (private leader socket, throwaway `TRACEARY_DB_PATH`, plugin 0.45.0 installed and enabled) recorded **zero** Traceary events — the store file was never even created. Debug logs show the traceary-grok plugin discovered with `has_hooks=true` and auto-trusted (User scope), yet the session hook dispatcher ran only global/user-level hooks (`global/...` entries for `session_start`, `user_prompt_submit`, `stop`, `session_end`); no plugin-provided hook executed in either mode. The adapter cannot capture events the host never delivers, so the Grok cells above are `available` until a Grok Build release dispatches plugin hooks again. Traceary does not synthesize `session_ended` (or any event) from process exit.

### Token usage capture (measured 2026-08-16, live store)

This is not a lifecycle hook. It is the additive `usage_observations` path.

| Host | Additive source | Measured coverage | Decision |
|---|---|---|---|
| Codex | `rollout_jsonl` | Healthy / continuous | Keep |
| Claude | `transcript_calls` (Stop-hook transcript walk) | 25 additive rows vs 666 Claude sessions since Aug 1 | Extractor now also accepts camelCase `inputTokens`/`outputTokens`. Interactive transcripts often omit both snake_case and camelCase usage; those turns stay availability-only. Not a silent zero — `report` discloses excluded/unavailable rows (#2018). |
| Grok | `headless_stream` only | 3 additive rows, none since 2026-07-23; 287 Grok sessions since Jul 24 | Grok `Stop` does not expose per-turn token counters. `stop_hook` rows are availability-only. Additive usage is limited to headless stream capture. |
| Kimi | `main_wire` | Last row 2026-08-13 matches last Kimi session | Not a regression. Known-token `main_wire` rows are excluded to avoid double-count vs stop_hook. |

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
- Codex CLI: official Codex CLI 0.145.0 hook reference (`SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`). Packaged config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
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
