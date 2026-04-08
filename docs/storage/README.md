# Storage model

[日本語](./README.ja.md)

Traceary stores its local state in a single SQLite database file.
This guide explains what gets written there, how the schema is organized, and what the current `gc` / backup defaults mean.

## Local-first layout

- default DB path: `~/.config/traceary/traceary.db`
- override: `--db-path` or `TRACEARY_DB_PATH`
- file permissions: Traceary creates the parent directory with `0700` and the DB file with `0600`
- no hidden remote service: the CLI, hooks, and MCP server all read and write the same local SQLite file

`traceary init` is optional. Any command that needs the store will create the DB and apply migrations on demand.

## Current schema

Traceary currently creates these tables:

### `events`

The append-only event stream. Every note, session boundary, review record, and command audit starts here.

Columns:

- `id`: event identifier
- `kind`: event kind such as `note`, `command_executed`, `session_started`, `session_ended`
- `agent`: logical actor such as `codex`, `claude`, `gemini`, or `manual`
- `session_id`: session grouping identifier
- `body`: human-facing message for the event
- `created_at`: RFC3339 timestamp
- `client`: ingestion path such as `cli`, `claude`, `codex`, `gemini`
- `repo`: current repository / workspace identifier when available

Important indexes:

- `idx_events_session_created_at` on `(session_id, created_at)`
- `idx_events_created_at` on `(created_at DESC, id DESC)`

### `command_audits`

Structured audit details for `command_executed` events.

Columns:

- `event_id`: primary key and foreign key to `events.id`
- `command_text`: captured command line
- `input_text`: stored command input payload
- `output_text`: stored command output payload
- `input_truncated`: whether Traceary truncated the stored input
- `output_truncated`: whether Traceary truncated the stored output

Because `command_audits.event_id` uses `ON DELETE CASCADE`, deleting an event through `gc` also deletes its audit payload.

## What Traceary does not store

Current non-goals:

- no background daemon metadata outside the SQLite file
- no hidden cloud sync or hosted history service
- no line-oriented export format as the primary persistence layer
- no schema migration registry outside the embedded SQL migrations in `schema/sqlite/migrations`

## Migrations and compatibility

- migrations are embedded in the binary from `schema/sqlite/migrations`
- the store is initialized before normal command execution, so upgrades apply migrations automatically
- backup restore copies the SQLite file first and then reruns store initialization so newer migrations can be applied

Traceary does not promise backward compatibility for arbitrary manual schema edits.
If you need a portable copy, use `traceary backup create` instead of editing the DB directly.

## `gc` defaults

`traceary gc` is retention-based cleanup for old events.

- default retention: `90` days (`--keep-days 90`)
- selection rule: delete rows where `created_at < cutoff`
- `--dry-run`: print only the candidate count
- after deletion, Traceary runs `VACUUM`

Practical implications:

- `gc` is opt-in; Traceary does not delete history automatically in the background
- a `gc` run removes matching events and their linked `command_audits`
- if you care about long-term history, take a backup before an aggressive cleanup

## Backup defaults

The supported backup story is intentionally simple:

- `traceary backup create` writes a compact SQLite backup file
- `traceary backup restore` copies that file into the destination DB path
- restore then reapplies migrations if the current binary knows newer schema versions

See the dedicated backup guide for machine transfer and destructive restore behavior:
[`../backup/README.md`](../backup/README.md)

## Operational transparency checklist

When you need to understand what Traceary is doing locally:

1. run `traceary doctor` to confirm the resolved DB path and writeability
2. inspect `schema/sqlite/migrations/` if you need the exact SQL
3. use `traceary backup create` before manual investigation or risky cleanup

