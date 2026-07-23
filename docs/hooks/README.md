# Hooks integration

[日本語](./README.ja.md)

Traceary ingests session boundaries, command audits, compact summaries, and prompt captures from Claude Code, Codex CLI, and Gemini CLI through hidden `traceary hook ...` runtime entrypoints.

Generated hook configs and packaged host hooks call those Go entrypoints directly. The shell scripts under `scripts/hooks/` remain compatibility wrappers for packaged assets and legacy hook installs; they are no longer the primary runtime implementation.

Current generated hook configs merge into existing supported client config files when possible, so adding Traceary hooks does not require destructive replacement by default.

If you want host-native packages instead of manual hook wiring, start with the [native integrations guide](../integrations/README.md).

> **v0.21 — Gemini CLI → Antigravity transition**: Gemini CLI is now a **legacy compatibility path**. Existing Gemini CLI hook installs continue to work unchanged. The successor host, Antigravity, is a supported Traceary hook client from v0.21.1 (capability diagnostics only in v0.21.0) — `traceary hooks install --client antigravity` wires its `hooks.json`, and Traceary captures session start and tool audits. When `Stop` supplies a readable `transcriptPath`, Traceary also captures final-turn prompt/transcript events. Current headless `agy --print` emits that `Stop` payload; see [Antigravity hooks and plugin](../integrations/antigravity.md) for the current state.

## Files

- `scripts/hooks/*.sh`: **canonical** compatibility wrappers that delegate to `traceary hook ...`
- `examples/hooks/claude.settings.json`: Claude Code example
- `examples/hooks/codex.hooks.json`: Codex CLI example
- `examples/hooks/gemini.settings.json`: Gemini CLI example
- packaged host integrations under `integrations/` and `plugins/` ship matching wrapper copies when the bundle format still expects script assets

Edit only `scripts/hooks/`, then run `go run ./cmd/repo-tooling integrations sync-hooks` so Claude / Codex / Gemini / Grok package copies stay byte-identical. `integrations verify` (CI) fails when a packaged copy drifts.

Traceary no longer installs portable hook-script copies under `~/.config/traceary/hook-scripts` for generated configs. Regenerate the hook config, or use a packaged integration, when you want the runtime entrypoint to stay `traceary hook ...`.

## Requirements

- `traceary` is installed and available in `PATH`, or `--traceary-bin` / `TRACEARY_BIN` points to the binary you want hooks to call
- `git` is optional; when available, Traceary prefers a normalized `remote.origin.url`, then falls back to the local git worktree root before using the raw hook `cwd`
- `bash` is still required only for packaged compatibility wrappers
- current hook examples assume Unix-like environments because the generated commands and compatibility wrappers target shell-based clients
- native Windows PowerShell / `cmd.exe` workflows are not supported today; use WSL or another POSIX-compatible environment if you need hooks on Windows
- generated hooks assume the target client can invoke external commands and pass JSON payloads/stdin in the formats described below

## Common environment variables

- `TRACEARY_BIN`: absolute path to the `traceary` binary when you are not relying on `PATH`
- `TRACEARY_DB_PATH`: explicit SQLite path when you do not want the default `~/.config/traceary/traceary.db`
- `TRACEARY_WORKSPACE`: explicit work-context string. Use this to override auto-detection.
- `TRACEARY_HOOK_STATE_DIR`: override where temporary session state is stored
- `TRACEARY_HOOK_STATE_KEY`: override the per-process state key when the default is not suitable
- `TRACEARY_HOOK_DEBUG`: when set, hook runtime errors are still suppressed, but the suppressed error is echoed to stderr

## Client differences

| Client | Settings file | Session start | Session end | Audit hook | Notes |
| --- | --- | --- | --- | --- | --- |
| Claude Code | `.claude/settings.json` or `~/.claude/settings.json` | `SessionStart` | `SessionEnd` | `PostToolUse` + `PostToolUseFailure` with `matcher: "Bash"`, `matcher: "mcp__.*"`, and the built-in tool matcher (`Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode`) | Anthropic's current docs define `Stop` as a per-response hook, not a session-end hook. |
| Codex CLI (`codex-cli 0.144.1`) | `~/.codex/hooks.json` | `SessionStart` | none (MCP `manage_session` / stale GC) | `PostToolUse` | The official Codex CLI 0.144.1 hook reference exposes `SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, and `Stop`. No dedicated `SessionEnd`; `Stop` fires per assistant response so Traceary treats it as a turn-boundary transcript, not a session end (#1170). |
| Gemini CLI (`gemini-cli 0.36.0`) *(legacy compatibility)* | `.gemini/settings.json` or `~/.gemini/settings.json` | `SessionStart` | `SessionEnd` | `AfterTool` with `matcher: "run_shell_command"` | Hooks are JSON-over-stdin / JSON-over-stdout and `SessionEnd` is best effort. Gemini CLI is the legacy path; Antigravity is the active successor (supported from v0.21.1). |
| Antigravity (supported from v0.21.1) | `.agents/hooks.json` or `~/.gemini/config/hooks.json` | `PreInvocation` (no `SessionStart`) | none (MCP `manage_session` / stale GC) | `PreToolUse` + `PostToolUse` paired across `stepIdx` (`run_command` only) | `hooks.json` is a top-level hook-group map (not the shared `{"hooks": {...}}` shape). `Stop` is a per-execution turn boundary, not a session end (#1170). See [Antigravity hooks and plugin](../integrations/antigravity.md). |

## What gets recorded

### Session hooks

`traceary hook session <client> <start|end|stop>` resolves a session ID in this order:

1. `session_id` from hook input
2. a previously stored ID in `TRACEARY_HOOK_STATE_DIR`
3. a new ID generated by `traceary session start`

The resolved session ID is persisted in a per-process state file so later audit, prompt, and compact hooks can reuse it when the client does not send `session_id` on every event.

### Audit hooks

`traceary hook audit <client>` records:

- `command`: `tool_input.command`
- `input`: compact JSON of `tool_input`
- `output`: compact JSON of `tool_response`, or `{error, is_interrupt}` for failure payloads

Secret-like values inside `input` / `output` are redacted by default before they are written to SQLite. Traceary treats this as best-effort protection, not a complete guarantee.

The hook exits successfully without recording anything when:

- `traceary` is not installed
- the hook payload has no `tool_input.command`
- a session ID cannot be resolved yet
- `TRACEARY_NO_AUDIT` is truthy, or the audited tool is a Traceary read/self-inspection command such as `traceary list --json`, `traceary sessions --snapshot --json`, MCP `list_events`, or MCP `search`

Use `TRACEARY_NO_AUDIT=1` when running ad-hoc Traceary investigations that would otherwise feed large JSON back into Traceary's own command-audit table. Traceary also skips known read-only self-inspection surfaces by default; write-like tools such as MCP `record_event` / `manage_memory` are still auditable.

### Prompt hooks

`traceary hook prompt <client>` records the user's instruction text as a `prompt` event so the full decision history — not just the tool audits downstream — is visible after the fact. Currently wired for:

- Claude Code via the `UserPromptSubmit` hook
- Codex CLI via the `UserPromptSubmit` hook (`codex-cli 0.121.0`+)

The hook reads `prompt` out of the payload and stores it as-is; secret redaction is intentionally not applied because recording the user's intent verbatim is the core value of this capture. Callers who want to sanitize prompts must do so upstream of Traceary.

The hook exits successfully without recording anything when:

- the payload does not contain a `prompt` field
- a session ID cannot be resolved yet

### SubagentStop (Claude Code 2026-01+)

`traceary hook subagent-stop claude` fires on Claude Code's `SubagentStop` hook — once per Task-tool subagent completion. The event is persisted as a `session_ended` entry prefixed with `[phase:subagent]` so the reviewer can recover the explicit subagent lifecycle boundary instead of inferring it from `agent_type` on `PostToolUse`. The marker keeps the main session's `session_ended` stream readable on its own.

### PreCompact (Claude Code 2026-01+)

`traceary hook compact claude pre-compact` fires on Claude Code's `PreCompact` hook — before Claude actually compacts the conversation. It persists a `compact_summary` event prefixed with `[phase:pre-compact]` so handoff / replay surfaces can tell the before-compact snapshot apart from the post-compact digest. The `loadCompactSummary` path skips pre-compact rows, so `session_handoff` / `memory_pack` still return the latest post-compact summary even when a cancelled compact cycle leaves a pre-compact snapshot behind as the newest `compact_summary` row.

### Codex compact and subagent hooks (Codex CLI 0.144.1+)

`PreCompact` and `PostCompact` call `traceary hook compact codex` with the corresponding phase. Codex supplies only `trigger` (`manual` or `auto`), not the resulting compacted summary, so Traceary stores phase-specific `compact_summary` boundary markers. `SubagentStart` and `SubagentStop` call the shared child-session runtime; `agent_id` is the correlation key and `agent_type` names the child agent.

## Installation flow

### Generate config from CLI

Use `traceary hooks print --client <claude|codex|gemini>` when you want a ready-to-paste config. `claude-code`, `codex-cli`, and `gemini-cli` are accepted aliases.

If you want the install/check/verify flow first, run `traceary hooks guide --client <claude|codex|gemini>`.

Examples:

- `traceary hooks print --client claude > .claude/settings.json`
- `traceary hooks print --client codex > ~/.codex/hooks.json`
- `traceary hooks print --client gemini > .gemini/settings.json`

By default the generated commands call `'traceary' 'hook' ...`, so the hook keeps following whichever stable `traceary` command is available in `PATH`.

Use `--traceary-bin` when you intentionally want to pin a specific binary path.

### Write config to the standard path

Use `traceary hooks install --client <claude|codex|gemini>` when you want Traceary to write the generated config file for you. `claude-code`, `codex-cli`, and `gemini-cli` are accepted aliases.

Examples:

- `traceary hooks install --client claude`
- `traceary hooks install --client codex`
- `traceary hooks install --client gemini`

Default destinations:

- Claude: `<project>/.claude/settings.json`
- Codex: `~/.codex/hooks.json`
- Gemini: `<project>/.gemini/settings.json`

### Claude PostToolUse matcher preset (`--matcher`)

For the Claude client, `hooks install` and `hooks print` accept `--matcher <preset>` to control which tool categories `PostToolUse` / `PostToolUseFailure` watch:

- `minimal` — `Bash` + `mcp__.*` only. Same set Traceary shipped before v0.8-6. Pick this when built-in tool captures generate too much `tail` / `timeline` volume for the current project.
- `default` (or omit `--matcher`) — Adds the v0.8-6b built-in tool list (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`). This is what packaged installs use.
- `all` — Replaces the built-in list with `.*` so every tool kind is captured. Intentional opt-in; this will include plugin- and project-specific tools that the default excludes.

The preset is ignored by the `codex` and `gemini` clients, whose audit hooks already run against every tool invocation.

When the Claude Code plugin is active, `hooks install --client claude` short-circuits with a skip notice regardless of `--matcher` to avoid double-registration. The plugin's own `hooks.json` stays on the default matcher; to change preset through the plugin path you need to disable the plugin (or fork its packaged `hooks.json`) before re-running `hooks install --matcher <preset>`.

### User-level install (`--global`)

Use `--global` to write the hooks to the user-level config instead of the per-project location:

- Claude: `~/.claude/settings.json`
- Gemini: `~/.gemini/settings.json`
- Codex: already user-level, so `--global` is a no-op and the standard `~/.codex/hooks.json` is used

`--global` is mutually exclusive with `--output`. User-level hooks apply to every project on the machine, which is the natural choice when you use Traceary across many repositories but do not want to commit `.claude/settings.json` per project.

If the destination already exists, Traceary stops with an error instead of overwriting it. Review the diff first, then rerun with `--force` only when replacing the existing file is intentional.
For supported JSON config files, `hooks install` first tries to merge Traceary-managed entries into the existing file while preserving unrelated settings. `--force` skips merge and replaces the file completely.

### Non-destructive migration (`--upgrade`)

Use `--upgrade` when a Traceary release adds a new hook event (for example `UserPromptSubmit`) and you want to catch it up without touching user-added hooks:

```
traceary hooks install --client codex --upgrade
```

The flag runs the same merge path as the default install but explicitly:

- never overwrites the destination file (mutually exclusive with `--force`),
- preserves every non-Traceary hook the user added,
- refreshes only the Traceary-managed entries (binary path changes, script-form → direct-form rewrites),
- strips Traceary-managed entries for events the current release no longer emits, so the Traceary footprint stays consistent with the running binary (reported as `Removed`),
- prints a per-event summary (`Added: UserPromptSubmit`, `Refreshed: …`, `Removed: …`, `Unchanged: …`),
- is idempotent — re-running after an upgrade reports `already up to date` and leaves the file byte-identical.

After `hooks install`, Traceary prints the matching `doctor` command so you can immediately verify the generated config in the same environment.

**Claude Code plugin interaction.** When the Traceary Claude Code plugin is enabled (detected via `enabledPlugins` in `~/.claude/settings.json`), `hooks install --client claude` skips writing the settings file and prints a notice — the plugin already delivers the same hooks, so installing both would record every audit event twice. Use `--force` only if you deliberately want both registrations (plugin development).

### Merge behavior and failure modes

`hooks install` can merge into an existing file when all of the following are true:

- the destination file already exists
- the destination is a JSON object at the root
- the existing `hooks` field is either absent or already shaped as `map[string][]hookMatcher`

`hooks install` fails instead of guessing when:

- the existing file is not valid JSON
- the JSON root is not an object
- the existing `hooks` field has a different shape than Traceary expects

In those cases, inspect the file yourself and only rerun with `--force` when replacing the file is truly acceptable.

### Timeout-kill preservation

Every hidden `traceary hook ...` entrypoint writes the exact host payload to a mode-`0600` record under `~/.config/traceary/hooks/spool/` before it touches SQLite. The record is atomically published and removed only after the hook operation returns successfully. SIGTERM cancels the command context; if the host kills a contended or slow hook, the already-published record remains available instead of the event disappearing silently.

Each hook persists, attempts, and clears its current delivery before it spends any remaining host timeout on backlog replay. Only then does it select and decode a bounded filename-ordered batch. A failed replay remains a v1 spool record but moves atomically behind records that have not yet been attempted, so one poisoned oldest record cannot starve later valid deliveries. `traceary doctor` reports retained or unreadable records through the `hook-spool` check; `traceary doctor --fix` drains at most 200 selected records per run and reports replayed, failed, and exact remaining counts. Inspect payloads under the spool directory before manual removal; Traceary does not delete records based on age alone.

SQLite waits at most 1 second for a busy writer. Every packaged host hook budget must exceed that wait. Gemini's generated and packaged hook timeout is 10 seconds, leaving time for the DB attempt to fail and the spool record to remain durable before the host deadline. This relationship is intentional: `SQLite busy_timeout < host hook timeout`.

Host-facing hook processes also apply an internal soft deadline (default **8s**, override with `TRACEARY_HOOK_SOFT_DEADLINE`, disable with `0`/`off`) slightly below the packaged 10s host budgets. Canceling ourselves first keeps spool retention deterministic on multi-GB stores whose cold open already costs several seconds. Detached workers (`memory-extract-worker`, `grok-transcript-worker`) are not soft-deadline bound. `traceary doctor` reports a `store-size` WARN when the live SQLite file exceeds ~1 GiB.

### Memory extraction queue

Session-end, turn-boundary, and subagent-stop hooks commit their primary event first, then enqueue memory auto-extraction under `~/.config/traceary/hooks/memory-extract/`. Jobs contain extraction criteria and operational metadata only, use mode `0600`, and coalesce repeated requests for the same database, session, and workspace. A detached internal worker calls the existing extractor after the host hook returns; success removes the job, while a failed or interrupted worker leaves it available for retry. Extraction therefore no longer consumes the host hook timeout or delays the primary event commit.

`traceary doctor` reports the pending count, previously failed count, terminal (attempt-cap exhausted) count, unreadable count, and oldest age through `hook-memory-extract`. It never prints extracted content or the stored criteria. Later hooks drain a bounded oldest-first batch across sessions (not only the same session key), and `traceary doctor --fix` forces a larger drain. Jobs that exhaust the total attempt ceiling become terminal and are garbage-collected after a retention window; persistent non-terminal failures should be investigated through debug logs.

## Troubleshooting

Run `traceary doctor --client <claude|codex|gemini|antigravity|grok>` when hooks or the local SQLite store do not behave as expected. Antigravity and Grok must be selected explicitly because they are not in the default client list.

The diagnostic command checks:

- DB path resolution and whether Traceary can initialize the store
- the expected client config location and whether it already contains Traceary-managed hooks
- optional Traceary config health (for example extra redaction patterns)
- for Claude, whether the Traceary plugin is enabled in the global `~/.claude/settings.json` and whether that overlaps with project-level Traceary hooks (double-registration is reported as `warn`)

Warnings are expected on first run before you install a host package or generated hooks. For example, a missing host config file is reported as `warn`, not `fail`.
`fail` is reserved for broken states such as DB access problems, unreadable config, or invalid config shape.

`doctor` does not execute the target client for you. It verifies file paths, DB access, and whether Traceary-managed hook entries are present, but it cannot prove that a third-party client will actually fire every hook in the same way as the validated local builds.

For SQLite concurrency expectations, PPID-based hook state caveats, and other known operational assumptions, see [`../operations/README.md`](../operations/README.md).

### Claude Code

1. Copy `examples/hooks/claude.settings.json` into `.claude/settings.json` and merge with existing settings.
2. Ensure the generated config points at your installed Traceary binary when you are not relying on `PATH`.
3. Start Claude Code in the project.
4. Run `traceary list --limit 10` after a short session to verify `session_started`, `session_ended`, `command_executed`, `compact_summary`, and `prompt` events.

### Codex CLI

1. Copy `examples/hooks/codex.hooks.json` into `~/.codex/hooks.json`.
2. Ensure `traceary` is available in `PATH`, or regenerate the config with `--traceary-bin` so it uses a pinned binary path.
3. Start a Codex session and inspect `traceary list --limit 10`.

Codex `Stop` fires after every assistant response, so Traceary records it as a turn-boundary transcript and keeps the session open (#1170). A Codex session ends via an explicit signal (MCP `manage_session`) or activity-aware stale GC. GC runs automatically after normal hook starts at most once every six hours per database; `traceary session gc` remains available for manual or scheduled fallback.

For bounded invocations, `traceary session run -- codex exec ...` is the explicit alternative. Traceary supervises the process and records one authoritative terminal reason without interpreting `Stop` as session completion. See [Lifecycle Events](./lifecycle-events.md#authoritative-one-shot-sessions).

### Gemini CLI *(legacy compatibility)*

> Gemini CLI is the legacy hook path; Antigravity is the active successor (supported from v0.21.1 — see below).

1. Merge `examples/hooks/gemini.settings.json` into `.gemini/settings.json` or `~/.gemini/settings.json`.
2. Ensure `hooksConfig.enabled` is already `true`. Traceary does not toggle this for you.
3. Start Gemini CLI and run at least one shell command.
4. Verify the resulting events with `traceary list --limit 10` or `traceary search "<command>"`.

### Antigravity

1. Install the Traceary hooks: `traceary hooks install --client antigravity --project-dir .` (workspace `.agents/hooks.json`) or `--global` (`~/.gemini/config/hooks.json`). The `agy` and `antigravity-cli` aliases also work.
2. Start an Antigravity conversation and run at least one `run_command` tool call.
3. Verify the resulting events with `traceary list --limit 10`.
4. Check the install with `traceary doctor --client antigravity --json`. Doctor reports `antigravity-capability` plus one check per install route (`antigravity-hooks-workspace`, `antigravity-hooks-user`, `antigravity-cli-plugin`) and an `antigravity-hooks` summary. Each route is optional: a missing workspace `.agents/hooks.json` is `skip`ped (not a warning) when the user-level or CLI-plugin route is healthy, and doctor only warns when no route registers the `traceary` group.

Antigravity has no `SessionStart`: Traceary starts the session idempotently from `PreInvocation`. Like Codex, `Stop` is a per-execution turn boundary, so the session stays open and ends via MCP `manage_session` or stale GC. Only `run_command` tool calls are audited (the command args from `PreToolUse` are paired with the result from `PostToolUse`). See the [Antigravity hooks and plugin guide](../integrations/antigravity.md).

When a Traceary CLI command fails, stderr is a plain `Error: ...` line. Hook wrappers can rely on the exit code and stderr text without stripping structured JSON logs.

## References

- [Lifecycle events](./lifecycle-events.md) — canonical Traceary event kinds emitted by hooks.
- Claude Code hooks reference: https://code.claude.com/docs/en/hooks
- Claude Code hooks guide: https://code.claude.com/docs/en/hooks-guide
- Gemini CLI hooks reference in the local install used for validation: `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`
