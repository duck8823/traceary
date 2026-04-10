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

- `--client` / `--agent` / `--repo`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_REPO` → `cli` / `manual` / detected repo
- `--session-id`: flag → `TRACEARY_SESSION_ID` → latest non-stale active session for the resolved repo → `default`

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`
- `--json`

Session resolution rules:

- explicit `--session-id` or `TRACEARY_SESSION_ID` wins
- otherwise Traceary reuses the latest non-stale active session for the resolved repo/work context
- when `remote.origin.url` is unavailable but the current directory is still inside a git worktree, Traceary falls back to the worktree root path as the work-context key
- if no repo/work context or no matching active session is found, Traceary falls back to the historical `default` session ID

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
- `--repo`
- `--id-only`
- `--json`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

Session resolution follows the same rules as `traceary log`.

## Read/query commands

### `traceary list`

List recent events.

`list` is the fast recent-history view. Use it when you already know the event kind / client / agent / session / repo filters you want. Switch to `search` when you need keyword matching or date-range filtering.

Useful flags:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--client`
- `--agent`
- `--repo`
- `--session-id`

### `traceary search [<query>]`

Search events by text and structured filters.

Useful flags:

- `--kind`
- `--client`
- `--agent`
- `--repo`
- `--session-id`
- `--since`
- `--until`
- `--limit`
- `--offset`
- `--json`

### `traceary show <event-id>`

Show one event in detail.

Useful flags:

- `--json`

### `traceary context`

Print a recent handoff/context bundle for another session or tool.

Alias:

- `traceary handoff`

Useful flags:

- `--session-id`
- `--repo`
- `--limit`
- `--json`

## Session commands

### `traceary session start`

Record a session start boundary and print the session ID.

Defaults:

- `--client` / `--agent` / `--repo`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_REPO` → `cli` / `manual` / detected repo
- `--session-id`: generate a new ID when omitted

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--parent-session-id`
- `--id-only`
- `--json`

### `traceary session end`

Record a session end boundary and print the resulting event ID.

Defaults:

- `--session-id`: flag → `TRACEARY_SESSION_ID`
- missing `--client` / `--agent` / `--repo` values are backfilled from the matching `session start` when available

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--summary`
- `--id-only`
- `--json`

### `traceary session list`

List session summaries.

The session list view surfaces session metadata such as `label`, `summary`, and `parent_session_id` together with status, duration, and aggregate counts.

Useful flags:

- `--repo`
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
- `--repo`
- `--json`

### `traceary session active`

Print the active session ID matching the current filters.

Useful flags:

- `--client`
- `--agent`
- `--repo`
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

Diagnose DB access, hook script availability, and client config integration.

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

### `traceary mcp-server`

Run the MCP server over stdio for AI client integration.

## Related docs

- onboarding and quick start: [`../../README.md`](../../README.md)
- environment variables and runtime assumptions: [`../environment/README.md`](../environment/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
