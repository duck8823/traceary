# Host Hook Coverage

[日本語](./host-coverage.ja.md)

This page tracks, per host AI agent, which Traceary [lifecycle events](./lifecycle-events.md) are wired to a real hook today, which hooks the host exposes but Traceary does not yet wire, and which are not supported by the host at all.

Legend:

- `●` wired in the packaged Traceary integration today
- `○` available in the host but not wired by Traceary yet
- `✕` not exposed by this host

**Last verified: 2026-04-26.** Refresh this page when bumping Traceary integration packages or when a host CLI release changes its hook surface.

## Lifecycle event → host hook matrix

| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.125 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Verification |
|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ○ `BeforeAgent` (planned in #806) | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` resume) | ✕ no compact hook in Codex 0.125 (upstream openai/codex#16098) | ○ `PreCompress` exists; no post-compress hook in Gemini 0.36.x (planned wiring in #807 — `PreCompress` marker only) | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ● `Stop` (best effort — Codex has no dedicated `SessionEnd`) | ● `SessionEnd` | `traceary list events --kind session_ended --limit 5` |

### Other host hooks Traceary does not wire today

| Host | Hook | Status | Note |
|---|---|---|---|
| Claude Code | `SubagentStart` (`PreToolUse matcher=Task\|Agent`) | ● wired (subagent capture, not a lifecycle event) | recorded as `note` body marker, not in the six lifecycle kinds |
| Claude Code | `SubagentStop` | ● wired (subagent capture) | same |
| Claude Code | `Notification`, `PreToolUse` (other matchers), `StopFailure` | ○ available | not in current packaged hooks |
| Codex CLI | `PreToolUse`, `PermissionRequest`, `Notification` | ○ available | not wired |
| Gemini CLI | `BeforeTool`, `BeforeToolSelection`, `BeforeModel`, `AfterModel`, `Notification` | ○ available | not wired |

## Per-host references

- Claude Code: https://code.claude.com/docs/en/hooks · packaged config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: upstream binary `codex-cli 0.125` — hook surface inferred from local install strings (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `Notification`, `PermissionDenied`, `UserPromptSubmit`, `Elicitation`). Compact hook tracking: openai/codex#16098. Packaged config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: hooks reference shipped with the local install at `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`. Packaged config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)

## Maintenance

This matrix is a manually curated snapshot. To refresh:

1. Bump or re-install the host CLI you want to re-check.
2. Diff the host's hook reference against the table above.
3. For each `●` cell, run the verification command and confirm a recent row exists in `~/.config/traceary/traceary.db`.
4. Update the **Last verified** date at the top of this page.

A daily drift check is wired through `/schedule` (see #814).
