# Host Hook Coverage

[日本語](./host-coverage.ja.md)

This page tracks, per host AI agent, which Traceary [lifecycle events](./lifecycle-events.md) are wired to a real hook today, which hooks the host exposes but Traceary does not yet wire, and which are not supported by the host at all.

Legend:

- `●` wired in the packaged Traceary integration today
- `○` available in the host but not wired by Traceary yet
- `✕` not exposed by this host

**Last verified: 2026-04-27 (Gemini CLI re-verified 2026-06-10 against 0.43.0; Antigravity added 2026-06-20 for v0.21.1).** Refresh this page when bumping Traceary integration packages or when a host CLI release changes its hook surface.

> **v0.21.1 note:** Gemini CLI hook coverage in this matrix is **legacy compatibility only**. Gemini CLI is the legacy Google AI agent host; Antigravity (`/Applications/Antigravity.app`) is the active successor. As of **v0.21.1, Antigravity is a supported hook client** with a packaged plugin against the documented public hook surface (`integrations/antigravity-plugin/`). See the [Antigravity hooks and plugin guide](../integrations/antigravity.md).

## Lifecycle event → host hook matrix

| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.144.1 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Antigravity (`antigravity-plugin`) | Verification |
|---|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | ● `PreInvocation` (idempotent, keyed by `conversationId`; Antigravity has no `SessionStart`) | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | ✕ no documented user-prompt hook | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | ● `PreToolUse` + `PostToolUse` (`run_command`; command args paired across the two events by `conversationId + stepIdx`) | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | ● `Stop` (`transcriptPath`, best-effort lenient JSONL scan) | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` resume) | ● `PreCompact` + `PostCompact` markers (`trigger` only; Codex exposes no compacted summary body) | ● `PreCompress` (marker only — Gemini exposes no post-compress hook with the resulting summary) | ✕ no documented compact hook | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ no host session-end signal — Codex `Stop` is a per-response turn boundary, not a session end (#1170); ends via MCP `manage_session` or stale GC | ● `SessionEnd` | ✕ no host session-end signal — Antigravity `Stop` is a per-execution boundary, not a session end (#1170); ends via MCP `manage_session` or stale GC | `traceary list events --kind session_ended --limit 5` |

> **Antigravity headless `agy --print`:** print mode emits `PreInvocation` and (for `run_command`) `PreToolUse`/`PostToolUse`, but the host emits no `Stop`, so the `transcript` row is **not** captured for print-mode runs — only on interactive runs where the host emits `Stop` with `transcriptPath`. See [Capture level in headless print mode](../integrations/antigravity.md#capture-level-in-headless-print-mode-agy---print). Hook events are recorded with `client=hook`, `agent=antigravity`, so verify them with `traceary list --agent antigravity` rather than `--client antigravity`.

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

## Per-host references

- Claude Code: https://code.claude.com/docs/en/hooks · packaged config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: official Codex CLI 0.144.1 hook reference (`SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`). Packaged config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: hooks reference shipped with the local install at `/opt/homebrew/Cellar/gemini-cli/0.43.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md` (no post-compress event in the documented hook surface; `PreCompress` is advisory-only and fires asynchronously before compression). Packaged config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)
- Antigravity: public hook surface documented at https://antigravity.google/assets/docs/antigravity-2-0/hooks.md and https://antigravity.google/assets/docs/editor/ide-hooks.md; plugin packaging at https://antigravity.google/assets/docs/cli/cli-plugins.md (verified 2026-06-20 JST). Packaged config: [`integrations/antigravity-plugin/hooks.json`](../../integrations/antigravity-plugin/hooks.json)

## Maintenance

This matrix is a manually curated snapshot. To refresh:

1. Bump or re-install the host CLI you want to re-check.
2. Diff the host's hook reference against the table above.
3. For each `●` cell, run the verification command and confirm a recent row exists in `~/.config/traceary/traceary.db`.
4. Update the **Last verified** date at the top of this page.

A daily drift check is wired through `/schedule` (see #814).
