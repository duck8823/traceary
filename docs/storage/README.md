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
- `command_wrapper`: verified wrapper basename when Traceary recognizes one (currently `rtk`); empty for direct commands
- `command_name`: normalized executable basename used for report aggregation (`rtk git ...` and `git ...` both use `git`)
- `exit_code`: captured exit code when available
- `failed`: compatibility failure flag derived from structured outcome evidence
- `failure_reason`: `none`, `exit_code`, `signal`, `timeout`, `hook_denied`, `host_error`, or `unknown`

Traceary normalizes only command structure it has verified. Direct executables use the first token basename, and only the observed `rtk <command>` / `rtk proxy <command>` grammar is unwrapped. It never executes or fully evaluates the shell text. A captured exit code of `0` is authoritative success even if input/output text contains words such as `failed`; report aggregation never parses payload text. Historical rows created before this schema use explicit `command_name=unknown` and `failure_reason=unknown` rather than reconstructing evidence that was not captured.

When `input_truncated` or `output_truncated` is true, the stored payload is already a bounded head/tail projection, the corresponding `*_original_bytes` column records the original size for new rows, and the body also includes an `original_bytes` marker for human-readable context. The omitted bytes are not recoverable from historical rows.

Because `command_audits.event_id` uses `ON DELETE CASCADE`, deleting an event through `gc` also deletes its audit payload.

### `sessions`

Session-level aggregates derived from start/end events and updated directly by session-oriented commands.

Key columns:

- `session_id`: session identifier
- `started_at`: session start time
- `ended_at`: session end time when the session has been closed
- `runtime_mode`: explicit lifecycle contract: `interactive`, `one_shot`, `resumed`, or `background`; historical rows migrate conservatively to `interactive`
- `terminal_reason`: single effective outcome: `success`, `failure`, `timeout`, `signal`, `aborted_stream`, or `legacy_unknown`; empty only while active or after an older binary writes the end marker
- `client`: client attribution for the session
- `agent`: agent attribution for the session
- `workspace`: auxiliary work-context identifier
- `label`: optional operator-facing label
- `summary`: optional session summary text
- `parent_session_id`: optional parent session link

Traceary never derives `one_shot` from an empty value. The first terminal reason is immutable: a same-reason delivery is an idempotent no-op, while a different reason fails closed and leaves the stored timestamp, summary, and reason unchanged. Pre-v0.30 ended rows use `legacy_unknown` rather than fabricated success or failure. The additive schema remains readable by older binaries; if one writes `ended_at` without a reason, the current binary restores that row as `legacy_unknown`.

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
- migration `000028` adds immutable `run_lineages` and `usage_observation_runs` tables without rewriting v27 usage rows; missing attribution remains unknown

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
- `memories`: physically delete `expired`, `superseded`, or `rejected` memories where `updated_at < cutoff`. `accepted` and `candidate` rows are not age-deleted. **Exception:** unreviewed auto-extracted candidates (`source IN (extracted, extracted-hidden, compact-summary)`) older than 14 days are **decayed to `expired`** (not hard-deleted) so they remain restorable until keep-days GC (#1368). Evidence/artifact refs cascade on physical delete; `supersedes_memory_id` pointers to deleted or about-to-decay rows are cleared first.
- `memory_edges`: delete closed edges where `valid_to < cutoff`; edges also cascade automatically when either endpoint memory is deleted.
- `all`: apply the policies in dependency order: events, sessions, memories, then memory_edges.

Practical implications:

- `gc` is opt-in; Traceary does not delete history automatically in the background
- use `--target events` for legacy event-only cleanup
- if you care about long-term audit history, take a backup before an aggressive cleanup
- for cold-row export with **verify-before-delete**, see [Archive-before-GC](./archive-before-gc.md) (#1309); full-file backup remains [Backup guide](../backup/README.md)

## Reversible historical content dedupe

**Requirement.** Early hook firings could write the same prompt/transcript twice. Current hook writes suppress only exact redeliveries backed by a stable host-native delivery ID; equal content without that evidence remains a legitimate distinct event. Historical heuristic duplicate groups remain and inflate `doctor`'s `content-event-reliability` warning and context size. Cleanup must be **explicit and reversible**: ordinary upgrade/migration must never move, delete, or rewrite `events` rows, and nothing may be hard-deleted without a recoverable trail (#1227).

**Command.** `traceary store dedupe content-events`

- default is a **dry-run** — it reports candidate groups and changes nothing;
- `--apply` quarantines the duplicates (moves them out of `events`);
- `--restore <run-id>` reverses an apply run;
- `--client codex` (default) scopes to Codex; `--client all` covers every agent. Hook duplicates are written with `client=hook`, so the selector filters by `agent`;
- `--strict` reports every exact duplicate group regardless of time gap;
- `--json` is available for dry-run, apply, and restore.

**Conceptual model.** A duplicate group is the identity tuple `kind, client, agent, session_id, workspace, source_hook, TrimSpace(body)` — the same identity the `content-event-reliability` diagnostic uses. This is a historical cleanup heuristic, not the runtime delivery identity: write-time redelivery suppression requires a stable host delivery ID plus a matching semantic fingerprint, and body equality alone never proves identity. Only `client='hook'` rows with `kind in (prompt, transcript)` participate; **command audits are never touched**. By default a group is eligible only when its members land near-simultaneously (within a 10s proximity window that clusters consecutive records pairwise, matching the diagnostic), so deliberate repeats far apart are excluded; `--strict` drops the window. The **canonical** row kept per group is the earliest parsed `created_at`, tie-broken by the smallest event id. `created_at` is parsed in Go as RFC3339Nano (never ordered lexically — `formatTimestamp` emits variable-width fractional seconds). A group containing a malformed timestamp is **skipped and reported**, never mutated.

**Responsibilities.** The CLI (`presentation/cli/store_dedupe.go`) parses flags and formats text/JSON. The usecase (`StoreManagementUsecase.DedupeContentEvents` / `RestoreContentEventDedupeRun`) mints the run id and `archived_at` timestamp on apply and validates input. The SQLite datasource (`StoreManagementDatasource`) does the transactional read/group/move and restore.

**Quarantine table.** Migration `000019` adds `event_content_dedupe_archive` (additive only — it never touches `events`). Each quarantined row preserves enough to restore the original `events` row verbatim: `id, kind, client, agent, session_id, workspace, body` (original, not normalized), `created_at, source_hook`, plus provenance `kept_event_id` (duplicate_of), `dedupe_run_id`, `archived_at`, `group_key`, and `reason`.

**Apply / restore semantics.**

- Apply runs in a single transaction (insert into archive + delete from `events`) and is **idempotent**: a second apply finds no duplicates left in `events` for an already-cleaned group, so it moves nothing.
- Restore is **all-or-nothing** and refuses to overwrite: if any original event id already exists in `events`, the whole restore fails and nothing changes.
- Because duplicates are moved *out* of `events`, normal `list`, `sessions --snapshot`, `doctor`, `context`, and MCP read surfaces stop showing them automatically.

**Rollback.** To undo an apply, run `traceary store dedupe content-events --restore <run-id>` (the run id is printed by `--apply` and stored on every archived row). If you need a belt-and-braces copy, take a `traceary store backup create` before `--apply`.

**Behavior tests.** Dry-run reporting and no-mutation, apply + idempotency, restore + overwrite refusal, malformed-timestamp skip, command-audit/non-hook exclusion, strict vs. proximity scope, and read-surface exclusion are covered in `infrastructure/sqlite/content_event_dedupe_test.go`; flag wiring and JSON/text output in `presentation/cli/store_dedupe_test.go`; run-id minting in `application/usecase/store_dedupe_test.go`.

## Body-free workspace identity diagnostics

Migration `000023` adds `hook_delivery_attempts`. Each row records only a delivery-record ID, attempted event ID, outcome (`accepted`, `conflict`, or `exact_redelivery`), origin (`runtime` or `backfill`), and timestamp. The migration seeds one accepted/conflict attempt from every pre-existing `hook_deliveries` row but marks it `backfill`; it never copies event bodies. Release-quality rates use runtime attempts only, so seeded rows cannot make an unmeasured rollout pass. Runtime writes add the delivery and its attempt in the same transaction.

The attempted event ID is Traceary's per-callback repository identity. A later host callback gets a new event ID and therefore a new attempt row. `INSERT OR IGNORE` suppresses only an internal retry of the same event object after a transaction race, preventing repository retry mechanics from inflating the host-delivery rate.

`session_workspace_aliases` stores explicit operator review metadata. An alias never rewrites `sessions.workspace`, `events.workspace`, or an observation's ingested relationship. The read projection changes a matching stored conflict to `explicit_alias` while the review exists, so removal is a complete rollback.

`traceary report workspace-identity` is read-only and does not run migrations or provenance catch-up. Initialize or migrate the store first with `traceary doctor`; an unready store fails with guidance. The default path does not load event bodies. `--include-heuristic` calls the existing dedupe planner with `Apply=false` and `MaxScanRows` set from the positive `--heuristic-limit`; a body-free count distinguishes a `partial` bounded sample from a `complete` one. Bounded apply is rejected, so cleanup remains a separate, unbounded, explicit, reversible command.

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
