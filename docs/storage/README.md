# Storage model

[日本語](./README.ja.md)

Traceary stores its local state in a single SQLite database file.
This guide explains what gets written there, how the schema is organized today, and what the current `gc` / backup defaults mean in practice.

## Local-first layout

- default DB path: `~/.config/traceary/traceary.db`
- override: `--db-path` or `TRACEARY_DB_PATH`
- file permissions: Traceary creates the parent directory with `0700` and the DB file with `0600`
- no hidden remote service: the CLI, hooks, and MCP server all read and write the same local SQLite file

`traceary store init` is optional. Any command that needs the store will create the DB and apply migrations on demand.

## Current schema

Traceary currently creates these tables:

### `events`

The append-only event stream. Notes, session boundaries, reviews, prompts, compact summaries, and command-audit wrapper events all start here.

Key columns:

- `id`: event identifier
- `kind`: event kind such as `note`, `command_executed`, `session_started`, `session_ended`, `prompt`, `compact_summary`
- `agent`: logical actor such as `codex`, `claude`, `gemini`, or `manual`
- `session_id`: session grouping identifier
- `body`: human-facing event message
- `created_at`: RFC3339 timestamp
- `client`: ingestion path such as `cli`, `claude`, `codex`, `gemini`, or `mcp`
- `workspace`: auxiliary work-context identifier when available

Important indexes:

- `idx_events_session_created_at` on `(session_id, created_at)`
- `idx_events_session_created_at_id_desc` on `(session_id, created_at DESC, id DESC)`
- `idx_events_created_at` on `(created_at DESC, id DESC)`
- `idx_events_workspace_created_at` on `(workspace, created_at)`

### `command_audits`

Structured audit details for `command_executed` events.

Key columns:

- `event_id`: primary key and foreign key to `events.id`
- `command_text`: captured command line
- `input_text`: stored command input payload
- `output_text`: stored command output payload
- `input_truncated`: whether Traceary truncated the stored input
- `output_truncated`: whether Traceary truncated the stored output
- `input_original_bytes`: original input byte count when `input_truncated` is true and known
- `output_original_bytes`: original output byte count when `output_truncated` is true and known
- `exit_code`: captured exit code when available
- `failed`: structural failure flag, set when a host reports a tool/command failure without a numeric exit code in the hook payload (e.g. Claude's `PostToolUseFailure`); `list --failures` matches `failed = 1` in addition to a non-zero `exit_code`

When `input_truncated` or `output_truncated` is true, the stored payload is already a bounded head/tail projection, the corresponding `*_original_bytes` column records the original size for new rows, and the body also includes an `original_bytes` marker for human-readable context. The omitted bytes are not recoverable from historical rows.

Because `command_audits.event_id` uses `ON DELETE CASCADE`, deleting an event through `gc` also deletes its audit payload.

### `sessions`

Session-level aggregates derived from start/end events and updated directly by session-oriented commands.

Key columns:

- `session_id`: session identifier
- `started_at`: session start time
- `ended_at`: session end time when the session has been closed
- `client`: client attribution for the session
- `agent`: agent attribution for the session
- `workspace`: auxiliary work-context identifier
- `label`: optional operator-facing label
- `summary`: optional session summary text
- `parent_session_id`: optional parent session link

Important indexes:

- `idx_sessions_started_at`
- `idx_sessions_repo_started_at`
- `idx_sessions_parent`

### `memories`

Durable-memory aggregates introduced in `v0.5.0`.

Key columns:

- `id`: durable memory identifier
- `type`: memory taxonomy such as `decision`, `constraint`, `preference`, `lesson`, `artifact`
- `scope_kind` / `scope_value`: typed scope flattened for persistence (`workspace`, `agent`, `session_family`)
- `fact`: distilled durable-memory text
- `status`: lifecycle status such as `candidate`, `accepted`, `rejected`, `superseded`, `expired`
- `confidence`: confidence value such as `low`, `medium`, `high`, `verified`
- `source`: source attribution such as `manual` or `extracted`
- `supersedes_memory_id`: previous memory replaced by this record, when present
- `expires_at`: expiry timestamp when present
- `created_at` / `updated_at`: lifecycle timestamps

Important indexes:

- `idx_memories_scope_status_updated`
- `idx_memories_type_status_updated`
- `idx_memories_supersedes_memory_id`

### `memory_evidence_refs`

Evidence references attached to a durable memory.

Key columns:

- `memory_id`: foreign key to `memories.id`
- `ordinal`: stable ordering within the memory
- `ref_kind`: reference type such as `event`, `session`, `url`, `file`, `issue`, `pr`
- `ref_value`: reference payload

### `memory_artifact_refs`

Artifact references attached to a durable memory.

Key columns:

- `memory_id`: foreign key to `memories.id`
- `ordinal`: stable ordering within the memory
- `ref_kind`: artifact type such as `file`, `url`, or `command`
- `ref_value`: artifact payload

## What Traceary does not store

Current non-goals:

- no background daemon metadata outside the SQLite file
- no hidden cloud sync or hosted history service
- no line-oriented export format as the primary persistence layer
- no schema migration registry outside the embedded SQL migrations in `schema/sqlite/migrations`

## Migrations and compatibility

- migrations are embedded in the binary from `schema/sqlite/migrations`
- store initialization runs before normal command execution, so upgrades apply migrations automatically
- backup restore copies the SQLite file first and then reruns store initialization so newer migrations can be applied

Traceary does not promise backward compatibility for arbitrary manual schema edits.
If you need a portable copy, use `traceary store backup create` instead of editing the DB directly.

## `gc` defaults

`traceary store gc` is retention-based cleanup for local store data.

- default retention: `90` days (`--keep-days 90`)
- default target: `all` (`--target all`)
- available targets: `events`, `sessions`, `memories`, `memory_edges`, `all`
- `--dry-run`: run the same deletion plan inside a rolled-back transaction and print only the candidate count
- after a non-dry-run deletion, Traceary runs `VACUUM`

Target policies:

- `events`: delete rows where `events.created_at < cutoff`; linked `command_audits` cascade via foreign keys.
- `sessions`: delete ended sessions where `COALESCE(ended_at, started_at) < cutoff` and no surviving events reference the session. Active sessions (`ended_at IS NULL`) are always protected.
- `memories`: delete only `expired` or `superseded` memories where `updated_at < cutoff`. `accepted`, manually-added `candidate`, and other statuses are kept indefinitely unless their status changes. **Exception:** auto-extracted candidates (`source IN (extracted, extracted-hidden)`) older than 14 days are also deleted in this pass — these rows are best-effort signal, not curated facts, and short retention keeps the inbox honest. Evidence/artifact refs cascade; `supersedes_memory_id` pointers to deleted rows are cleared first to preserve FK integrity.
- `memory_edges`: delete closed edges where `valid_to < cutoff`; edges also cascade automatically when either endpoint memory is deleted.
- `all`: apply the policies in dependency order: events, sessions, memories, then memory_edges.

Practical implications:

- `gc` is opt-in; Traceary does not delete history automatically in the background
- use `--target events` for legacy event-only cleanup
- if you care about long-term audit history, take a backup before an aggressive cleanup

## Backup defaults

The supported backup story is intentionally simple:

- `traceary store backup create` writes a compact SQLite backup file
- `traceary store backup restore` copies that file into the destination DB path
- restore then reapplies migrations if the current binary knows newer schema versions

See the dedicated backup guide for machine transfer and destructive restore behavior:
[`../backup/README.md`](../backup/README.md)

## Operational transparency checklist

When you need to understand what Traceary is doing locally:

1. run `traceary doctor` to confirm the resolved DB path and writeability
2. inspect `schema/sqlite/migrations/` if you need the exact SQL
3. use `traceary store backup create` before manual investigation or risky cleanup
