# CLI reference

[日本語](./README.ja.md)

This page is the stable command reference for Traceary's public CLI surface.
Use it together with the quick-start section in `README.md`.

## Common conventions

- DB path resolution order: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- Mutating commands print human-friendly text by default
- Commands that create an event or session identifier support `--id-only` when scripts want the raw identifier
- Commands that support structured output expose `--json`

## Core event commands

### `traceary log <message>`

Append a note event.

Defaults:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / detected workspace
- `--session-id`: flag → `TRACEARY_SESSION_ID` → latest non-stale active session for the resolved workspace → `default`

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`

Session resolution rules:

- explicit `--session-id` or `TRACEARY_SESSION_ID` wins
- otherwise Traceary reuses the latest non-stale active session for the resolved workspace
- when `remote.origin.url` is unavailable but the current directory is still inside a git worktree, Traceary falls back to the worktree root path as the work-context key
- if no workspace could be resolved or no matching active session is found, Traceary falls back to the historical `default` session ID

> **Note:** `log` and `audit` accept any `--session-id` value without validating whether the session actually exists. This is by design — hooks record events at high frequency and the extra DB lookup per write would add unacceptable overhead. If you pass a nonexistent session ID, the event is still recorded; it will simply not appear in session-scoped queries.

### `traceary audit <command> [<input>] [<output>]`

Record a command execution audit event.

Input styles:

- positional with command only: `traceary audit "go test ./..."`
- positional: `traceary audit "go test ./..." '{}' '{}'`
- named: `traceary audit --command "go test ./..." --input '{}' --output '{}'`

Useful flags:

- `--command`
- `--input`
- `--output`
- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

Session resolution follows the same rules as `traceary log`.

## Read/query commands

### `traceary list`

List recent events.

`list` is the fast recent-history view. Use it when you already know the event kind / client / agent / session / workspace filters you want. Switch to `search` when you need keyword matching or date-range filtering.

Default text output is the same compact single-line shape as `tail` (`HH:MM:SS  kind  sess=<first-8>  ws=<basename>  message`, local time, no header). Pass `--wide` for the legacy tab-separated seven-column format, or `--utc` to force UTC timestamps. `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte. `--json` is unchanged. Use `--fields ts,kind,message` to pick which compact columns are shown; precedence is `--fields` > preset fields > `read.fields` in `~/.config/traceary/config.json` > built-in default. `--fields` cannot be combined with `--wide`. Supported fields: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`. Use `--preset <name>` to apply a saved view: built-in presets are `failures`, `prompts-only`, `compact-summaries`; user-defined entries in `read.presets` can override built-in names and explicit filter flags (`--kind`, `--failures`, `--workspace`, etc.) still win over a preset. Presets ignore `--wide` / `--json` for field overrides but still apply filters. Use `--color=auto|always|never` to toggle ANSI highlighting of compact rows (defaults to `auto`, honours the `NO_COLOR` env variable, and is ignored by `--wide` and `--json`). When highlighting is on, failed `command_executed` rows turn red+bold, `prompt` rows become cyan, `compact_summary` rows become magenta, and `session_started` / `session_ended` rows are dimmed.

Useful flags:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`

### `traceary tail`

Follow new events as they arrive.

`tail` is the live observation view. It prints a recent backlog first and then keeps following new matching events from the local store. Use it when you want to confirm that hooks are firing, that the expected session/workspace is receiving writes, or that failures are surfacing in real time. Unlike `list`, it does not exit after one snapshot. Unlike `search`, it does not perform keyword matching. Unlike `handoff`, it stays at the raw event-stream layer rather than assembling working memory.

Default text output is a compact single-line row (`HH:MM:SS  kind  sess=<first-8>  ws=<basename>  message`) that fits within ~100 columns and uses local time. Pass `--wide` for the legacy tab-separated seven-column shape, or `--utc` to force UTC timestamps in either text mode. `--wide --utc` reproduces the pre-v0.6.1 format byte-for-byte for scripts that parse it. `--json` emits newline-delimited JSON objects (one event per line) so pipelines can consume the stream incrementally; timestamps in JSON remain RFC3339 and are unaffected by `--utc`.

> The compact session ID (`sess=<first-8>`) is intended for human scanning only. For machine processing, use `--wide --utc` or `--json`.

Use `--fields ts,kind,message` to override the compact column order (precedence: flag > preset fields > `read.fields` in config.json > built-in default). `--fields` cannot be combined with `--wide`; see `traceary list` above for the full list of supported fields. Use `--preset <name>` for saved views (built-in: `failures` / `prompts-only` / `compact-summaries`; user-defined in `read.presets`). Use `--follow-session <prefix>` (minimum 8 runes) to scope the tail to one session — the value matches session ids by prefix so it is safe to paste from `traceary session list` output.

Useful flags:

- `--kind`
- `--limit`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--follow-session`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--failures`

### `traceary search [<query>]`

Search events by text and structured filters.

Text results use the same compact single-line format as `list` / `tail` (local time by default). Pass `--wide` for the legacy seven-column table, or `--utc` to force UTC timestamps. `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte. `--json` is unchanged. Use `--fields ts,kind,message` to override the compact column order (precedence: flag > preset fields > `read.fields` in config.json > built-in default); `--fields` cannot be combined with `--wide`, and the supported field list is shown under `traceary list` above. Use `--preset <name>` for saved views; a preset with filters can make a search with no free-text query valid because its filters count as constraints.

Useful flags:

- `--kind`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--since`
- `--until`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`

### `traceary timeline`

Show work timeline with gap-based block detection and per-workspace activity summaries.

`timeline` groups recent events into contiguous work blocks separated by idle gaps (default: 15 minutes) and prints one aligned sub-row per workspace inside each block. The per-workspace activity summary is picked using the fallback chain **`compact_summary` → first `prompt` → kind counts**, so whichever signal exists for that workspace in the block lights up the line. Default text output uses local time; pass `--utc` for UTC. `--json` extends the block schema with a `workspace_breakdown` array (`{workspace, event_count, kind_counts, summary, summary_source}`) — existing consumers keep working unchanged.

Useful flags:

- `--workspace`
- `--from`
- `--to`
- `--gap` (idle gap threshold in minutes)
- `--limit`
- `--json`
- `--utc`

### `traceary show <event-id>`

Show one event in detail.

Useful flags:

- `--json`

### `traceary context`

Print raw recent context events for another session or tool.

Useful flags:

- `--session-id`
- `--workspace`
- `--limit`
- `--json`

### `traceary handoff`

Print a structured working-memory handoff summary built from session metadata, recent commands, compact summaries, and accepted durable memories.

Useful flags:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary session handoff`

Session-scoped entry point for the same structured handoff output as `traceary handoff`.

Useful flags:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary compact-summary`

Print a compact context-resumption pointer assembled from the same working-memory pack used by `traceary handoff`, but compressed for prompt injection and compact/clear recovery.

Useful flags:

- `--session-id`
- `--workspace`
- `--recent`

## Durable memory commands

### `traceary memory list`

List durable memories. When no explicit scope flag is set, `memory list` defaults to the resolved workspace scope.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory search [<query>]`

Search durable memories by text and structured filters. At least one query or filter is required.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory show <memory-id>`

Show one durable memory in detail, including evidence and artifact references.

Useful flags:

- `--json`

### `traceary memory remember`

Record an accepted durable memory directly.

Useful flags:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--id-only`
- `--json`

### `traceary memory propose`

Record a candidate durable memory that still needs review.

Useful flags are the same as `memory remember`, except `--confidence` is ignored.

### `traceary memory extract`

Extract candidate durable memories from a target session using session summaries, compact summaries, prompt events, and note/review signals. Extraction is candidate-only: Traceary never auto-accepts the extracted memories. Prompt events are optional; missing prompt or compact-summary events simply reduce the available signals. When `--session-id` is omitted, Traceary resolves the active session first and then falls back to the latest matching session in the workspace. `Feedback:` / `Correction:` labels are preserved by mapping them into the current minimal durable-memory taxonomy as `preference` candidates. Persisted candidates go through the same sanitization/redaction path as other durable-memory writes before they are stored.

Useful flags:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--json`

### `traceary memory accept <memory-id>`

Accept a candidate durable memory.

Useful flags:

- `--confidence`
- `--id-only`
- `--json`

### `traceary memory reject <memory-id>`

Reject a candidate durable memory.

Useful flags:

- `--id-only`
- `--json`

### `traceary memory supersede <memory-id>`

Replace an accepted durable memory with a new accepted memory. Omitted `--type` and scope flags inherit from the current memory.

Useful flags:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--id-only`
- `--json`

### `traceary memory expire <memory-id>`

Expire an active durable memory.

Useful flags:

- `--at`
- `--id-only`
- `--json`

## Session commands

### `traceary session start`

Record a session start boundary and print the session ID.

Defaults:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / detected workspace
- `--session-id`: generate a new ID when omitted

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--parent-session-id`
- `--id-only`
- `--json`

### `traceary session end`

Record a session end boundary and print the resulting event ID.

Defaults:

- `--session-id`: flag → `TRACEARY_SESSION_ID`
- missing `--client` / `--agent` / `--workspace` values are backfilled from the matching `session start` when available

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--summary`
- `--id-only`
- `--json`

### `traceary session list`

List session summaries.

The session list view surfaces session metadata such as `label`, `summary`, and `parent_session_id` together with status, duration, and aggregate counts.

Useful flags:

- `--workspace`
- `--agent`
- `--label`
- `--from`
- `--to`
- `--limit`
- `--offset`
- `--json`

### `traceary session label <label-text>`

Set or update a session label.

Defaults:

- `--session-id`: flag → `TRACEARY_SESSION_ID`

Useful flags:

- `--session-id`
- `--db-path`

### `traceary session latest`

Print the latest session ID matching the current filters.

`latest` means the session whose most recent lifecycle boundary (`session start` or `session end`) is newest among the matches.

Useful flags:

- `--client`
- `--agent`
- `--workspace`
- `--json`

### `traceary session active`

Print the active session ID matching the current filters.

Useful flags:

- `--client`
- `--agent`
- `--workspace`
- `--stale-after`
- `--allow-stale`
- `--json`

## Hooks and diagnostics

### `traceary completion <bash|zsh|fish|powershell>`

Generate shell completion scripts for interactive use.

### `traceary hooks print`

Print generated hook configuration for a supported client.

Supported clients: `claude`, `codex`, `gemini`
Aliases: `claude-code`, `codex-cli`, `gemini-cli`

Useful flags:

- `--client`
- `--traceary-bin`

### `traceary hooks install`

Install generated hook configuration into the standard client config path.

Useful flags:

- `--client`
- `--project-dir`
- `--traceary-bin`
- `--output`
- `--force`

### `traceary hooks guide`

Print the recommended install/check/verify steps for a supported client.

Useful flags:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

Diagnose DB access, generated hook configuration presence, and client config integration.

`warn` means Traceary found a first-run / not-configured-yet state, such as a missing host config file before hooks are installed.
`fail` means Traceary found a broken runtime state, such as DB access problems or unreadable / invalid config.
Only `fail` checks make `traceary doctor` exit non-zero.

Alias:

- `traceary status`

Useful flags:

- `--client`
- `--project-dir`
- `--json`

## Backup and maintenance

### `traceary init`

Explicitly create the DB and apply migrations up front.
This command is optional because normal commands initialize the store on demand.

### `traceary backup create`

Create a compact SQLite backup file.

Useful flags:

- `--output`
- `--db-path`
- `--force`

### `traceary backup restore`

Restore the DB from a backup file.

Useful flags:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary gc`

Delete old events and compact the SQLite store.

Useful flags:

- `--before`
- `--keep-days`
- `--vacuum`
- `--dry-run`

## Integration commands

### `traceary integration codex install` (deprecated)

Install the packaged Codex plugin from a local repository checkout into:

- `~/.agents/plugins`
- `~/.codex/plugins/cache/...`
- `~/.codex/config.toml`
- `~/.codex/hooks.json`

**Deprecated**: prefer Codex's official `/plugins` flow (run `codex` inside the repository → `/plugins` → `Traceary Plugins` → `Traceary`). This command will be removed no earlier than v0.8.0; see the [Codex plugin guide](../integrations/codex-plugin.md) for the full migration recipe.

Useful flags:

- `--repo-root`
- `--codex-home`
- `--marketplace-root`
- `--traceary-bin`

### `traceary integration codex uninstall`

Remove the Traceary-managed Codex plugin cache, plugin config entry, and hook entries while preserving unrelated local Codex settings. Kept as the recommended cleanup step for users migrating away from the deprecated `install` command above.

Useful flags:

- `--codex-home`
- `--marketplace-root`

### `traceary mcp-server`

Run the MCP server over stdio for AI client integration.

## Related docs

- onboarding and quick start: [`../../README.md`](../../README.md)
- environment variables and runtime assumptions: [`../environment/README.md`](../environment/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
