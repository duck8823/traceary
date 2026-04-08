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

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

Session resolution rules:

- explicit `--session-id` or `TRACEARY_SESSION_ID` wins
- otherwise Traceary reuses the latest non-stale active session for the resolved repo/work context
- if no repo/work context or no matching active session is found, Traceary falls back to the historical `default` session ID

### `traceary audit [<command> <input> <output>]`

Record a command execution audit event.

Input styles:

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
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

Session resolution follows the same rules as `traceary log`.

## Read/query commands

### `traceary list`

List recent events.

Useful flags:

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

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

### `traceary session end`

Record a session end boundary and print the resulting event ID.

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

### `traceary session latest`

Print the latest session ID matching the current filters.

Useful flags:

- `--client`
- `--agent`
- `--repo`

### `traceary session active`

Print the active session ID matching the current filters.

Useful flags:

- `--client`
- `--agent`
- `--repo`
- `--stale-after`
- `--allow-stale`

## Hooks and diagnostics

### `traceary completion <bash|zsh|fish|powershell>`

Generate shell completion scripts for interactive use.

### `traceary hooks print`

Print generated hook configuration for a supported client.

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
