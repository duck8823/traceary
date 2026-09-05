# Storage model

[日本語](./README.ja.md)

Traceary stores its local state in a single SQLite database file.
This guide explains what gets written there, how the schema is organized today, and what the current `compact` / backup defaults mean in practice.

## Local-first layout

- default DB path: `~/.config/traceary/traceary.db`
- override: `--db-path` or `TRACEARY_DB_PATH`
- file permissions: Traceary creates the parent directory with `0700` and the DB file with `0600`
- no hidden remote service: the CLI and hooks read and write the same local SQLite file

The store is created on demand. Any command that needs the store will create the DB and apply implicit migrations. Data-dependent offline migrations require `traceary doctor --fix`.

## Current schema

Traceary currently creates these tables:

### `events`

The append-only event stream. Notes, session boundaries, reviews, prompts, compact summaries, and command-audit wrapper events all start here.

Key columns:

- `id`: event identifier
- `kind`: event kind such as `note`, `command_executed`, `session_started`, `session_ended`, `prompt`, `compact_summary`
- `agent`: logical actor such as `codex`, `claude`, `gemini`, or `manual`
- `session_id`: session grouping identifier
- `body`: human-facing event message for non-audit kinds. For `command_executed` rows the retained execution record lives in `command_audits`. New writes leave this column empty. `traceary store compact` clears leftover historical bodies when an audit row exists (`#1853`). `traceary log --kind command_executed` bodies without an audit row are kept.
- `created_at`: RFC3339 timestamp
- `client`: ingestion path such as `cli`, `claude`, `codex`, `gemini`, or `mcp`
- `workspace`: auxiliary work-context identifier when available

Important indexes (lexical `created_at_norm` family; raw `created_at` duplicates were dropped in migration 000071):

- `idx_events_created_at_norm_id_desc` on `(created_at_norm DESC, id DESC)`
- `idx_events_session_created_at_norm_id_desc` on `(session_id, created_at_norm DESC, id DESC)`
- `idx_events_workspace_created_at_norm_id_desc` on `(workspace, created_at_norm DESC, id DESC)`
- `idx_events_source_hook_created_at_norm_id_desc` on `(source_hook, created_at_norm DESC, id DESC)`

### `command_audits`

Structured audit details for `command_executed` events. This is the retained execution record. `store compact` removes a leftover composed copy from `events.body` when this row exists.

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
- `output_metadata`: nullable canonical JSON for a metadata-only capture (`capture`, `paths`, `bytes`, `sha256`, `truncated`). Historical rows stay NULL

Read-only tool audits store access facts only. For a successful classified read/list/grep (Claude `Read`/`Grep`/`Glob`/`NotebookRead`/`WebFetch`, Grok `read_file`/`grep`/`list_dir`, Kimi `Read`/`Grep`/`Glob`/`ReadMediaFile`, Gemini `read_file`/`read_many_files`/`list_directory`/`glob`/`search_file_content`), new rows store `output_text=''`, `output_truncated=0`, `output_original_bytes=0`, and put the redacted-response size in `output_metadata.bytes`. The sha256 is over the redacted response. Failed or denied read-only calls keep full (bounded) output. Existing rows are never rewritten. Archive v1 segments do not carry `output_metadata`. See [capture contract](../integrations/capture-contract.md).

New writes set `failed` from `failure_reason.IsFailure()`. A structural hook failure with no exit code is stored as `host_error`, not as `unknown`. Rows with `failed=1` and `failure_reason=unknown` are pre-classifier history (schema default before 2026-07-22); restore keeps them, and new writes cannot create them. See [failed-flag meaning](../research/failed-flag-meaning.md).

Traceary normalizes only command structure it has verified. Direct executables use the first token basename, and only the observed `rtk <command>` / `rtk proxy <command>` grammar is unwrapped. It never executes or fully evaluates the shell text. A captured exit code of `0` is authoritative success even if input/output text contains words such as `failed`; report aggregation never parses payload text. Historical rows created before this schema use explicit `command_name=unknown` and `failure_reason=unknown` rather than reconstructing evidence that was not captured.

When `input_truncated` or `output_truncated` is true, the stored payload is already a bounded head/tail projection and the corresponding `*_original_bytes` column records the original size for new rows. Search indexes `command_text` / `input_text` / `output_text` independently of `events.body`, so clearing the composed body does not drop searchable audit text. The omitted truncated bytes are not recoverable from historical rows.

Because `command_audits.event_id` uses `ON DELETE CASCADE`, event row deletion also deletes the corresponding audit payload.

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
- store initialization runs before normal command execution, so upgrades apply non-offline migrations automatically
- data-dependent offline migrations (035, 045, 076, 078, 079, 080, 081, 082, 083) are not applied implicitly; run `traceary doctor --fix`
- backup restore copies the SQLite file first and then reruns store initialization so newer non-offline migrations can be applied
- migration `000028` added immutable `run_lineages` and `usage_observation_runs` tables without rewriting v27 usage rows; missing attribution remains unknown
- migration `000079` drops `run_lineages` after rebuilding `usage_observation_runs` without that foreign key, and raises `minimum_reader_version` to 35. Non-empty stores apply it on a candidate via `traceary doctor --fix`; dropping remaining rows requires `--approve-drop N:<hex>`
- migration `000083` drops `body_availability`, `raw_body_retention_*`, and `session_orphan_ranges`, and raises `minimum_reader_version` to 39. Non-empty stores apply it on a candidate via `traceary doctor --fix`. Rows whose `body_availability` was `unavailable_retention` cannot be recovered by any binary, including 0.48.2. Preflight and `traceary doctor` report the count, a bounded sample, and the digest of the sorted affected id set. When the count is greater than zero, `--approve-unavailable-retention N:<hex>` is required; a missing, stale, or drifted token leaves the candidate unmodified and the live store untouched. Count 0 needs no approval. Legacy marker text `[traceary:body-unavailable:retention]` that remains in `events.body` is ordinary body text: search and display treat it verbatim.

Traceary does not promise backward compatibility for arbitrary manual schema edits.
If you need a portable copy, use `traceary store backup create` instead of editing the DB directly.

## `compact` defaults

`traceary store compact` rewrites the store file. During the copy it drops the
retired search index family and vacuums into a new file. Event bodies and
`command_audits` command / input / output text stay as written plaintext.
Compact is cover-free: it does not write mechanical summaries, does not
discard transcript bodies, and has no `--refuse-unrefined` flag.

- `--keep-days 90` is the `--archive` keep window, not a body-discard cutoff
- physical reclamation is the rewrite itself; it is not an in-place `VACUUM`
- leftover `command_executed` bodies that already have a `command_audits` row may be cleared (`released_command_body_bytes`)
- File retention of archive/backup artifacts is `store compact --retention-plan` / `--retention-apply`. The search-projection family is dropped by offline migration 80 (`traceary doctor --fix` on a non-empty store), not by compact. `--projection-rebuild` / `--projection-abort` are unknown flags.

**Reclaim warning.** After a non-hook command, Traceary may print `TRACEARY: store can reclaim about <size>; run traceary store compact` on stderr, at most once every 24 hours per store (tracked in `<db>.reclaim-warn`). It appears only when the store's free-page list — `PRAGMA freelist_count × PRAGMA page_size`, the same O(1) signal `traceary doctor` reports as `reclaimable=` — is at least `compact.reclaim_warn_bytes` (default 1 GiB) **and** at least 10 % of the store. File size alone never triggers it: a 14 GiB store with an empty freelist stays quiet, because a rewrite would return nothing to the filesystem. `doctor` uses the same signal and the same 10 % ratio with a lower floor of 256 MiB, so it can report reclaimable pages that the trailer stays quiet about; it never reports fewer. Set `compact.reclaim_warn_bytes` to `0` to silence the trailer. When the store cannot be read cheaply (a writer holds it, or the read exceeds 500 ms) nothing is printed — `traceary doctor` is where an unknown store-growth signal is reported.

Free pages are the bytes a rewrite can return to the filesystem from SQLite's freelist. Compact no longer discards transcript bodies, so the O(1) freelist signal is the reclaim estimate. `traceary store compact --dry-run` is the `--archive` plan/count surface, not a body-discard preview.

### Derived generation disk bound

The search-projection family was deleted in v0.49.0 (#2319). There is no generation lifecycle, no `--index-family-bytes` budget, and no doctor `search-projection-terminal-rows` check. Compact JSON does not report an encode step. The physical DROP is offline migration 80.

Target policies for `--archive` / GC (not the default compact rewrite):

- `events`: never deletes event rows and never discards or rewrites event bodies. Event skeletons, `prompt` bodies, `transcript` bodies, all other kinds, and `command_audits.command_text` / `input_text` remain.
- `sessions`: delete ended sessions where `COALESCE(ended_at, started_at) < cutoff` and no surviving events reference the session. Active sessions (`ended_at IS NULL`) are always protected.
- `memories`: physically delete `expired`, `superseded`, or `rejected` memories where `updated_at < cutoff`. `accepted` and `candidate` rows are not age-deleted. **Exception:** unreviewed auto-extracted candidates (`source IN (extracted, extracted-hidden, compact-summary)`) older than 14 days are **decayed to `expired`** (not hard-deleted) so they remain restorable until keep-days GC (#1368). Evidence/artifact refs cascade on physical delete; `supersedes_memory_id` pointers to deleted or about-to-decay rows are cleared first.
- `memory_edges`: delete closed edges where `valid_to < cutoff`; edges also cascade automatically when either endpoint memory is deleted.
- `all`: apply the policies in dependency order: events, sessions, memories, then memory_edges. Because events now survive, `delete_empty_sessions.sql` is no longer fed by event deletion.

Practical implications:

- `store compact` is operator-initiated; Traceary does not discard history automatically in the background
- event bodies stay as written until the row itself is removed (archive `--delete-after-verify` or an explicit delete)
- if you care about long-term audit history, take a backup before an aggressive cleanup
- for cold-row export with **verify-before-delete**, see [Archive-before-GC](./archive-before-gc.md) (#1309); full-file backup remains [Backup guide](../backup/README.md)

## Per-record storage accrual

Each event row accrues storage permanently. There is no body-discard path: compact rewrites the file (freelist / `VACUUM INTO`) and may clear leftover `command_executed` bodies that already have a `command_audits` row. It does not replace transcript bodies with a retention marker.

The following always remain:

- `command_audits.command_text`, `input_text`, `output_text` — the structured audit record for `command_executed` events
- `prompt` and `transcript` event bodies
- event skeletons (id, kind, session_id, created_at, and other metadata columns) for all kinds

Measured composition on the reference corpus: roughly **2.67 KiB/event** plus the former discardable **0.35 KiB/event** of covered `transcript` bodies now stay in the live store, against a roughly **4 KiB/event** ceiling. Body discard cannot converge that ceiling. The permanently-accruing share grows with the number of events regardless of how often compact runs.

## Reversible historical content dedupe

**Requirement.** Early hook firings could write the same prompt/transcript twice. Current hook writes suppress only exact redeliveries backed by a stable host-native delivery ID; equal content without that evidence remains a legitimate distinct event. Historical heuristic duplicate groups remain and inflate `doctor`'s `content-event-reliability` warning and context size. Cleanup must be **explicit and reversible**: ordinary upgrade/migration must never move, delete, or rewrite `events` rows, and nothing may be hard-deleted without a recoverable trail (#1227).

**Command.** `traceary store dedupe content-events` (CLI surface removed in #1872; the bullets describe the port/usecase, not a shipped command.)

- default is a **dry-run** — it reports candidate groups and changes nothing;
- `--apply` quarantines the duplicates (moves them out of `events`);
- `--restore <run-id>` reverses an apply run;
- `--purge <run-id>` ends a run's rollback window, dropping the archived rows so their bytes are actually reclaimed. SQLite returns the pages to its free list, so run `VACUUM` afterwards to return them to the filesystem;
- `--list-runs` reports the quarantine runs still held in the archive, newest first;
- `--client codex` (default) scopes to Codex; `--client kimi` scopes to Kimi; `--client all` covers every agent. Hook duplicates are written with `client=hook`, so the selector filters by `agent`;
- `--strict` reports every exact duplicate group regardless of time gap;
- `--json` is available for dry-run, apply, restore, purge, and run listing.

**Conceptual model.** A duplicate group is the identity tuple `kind, client, agent, session_id, workspace, source_hook, TrimSpace(body)` — the same identity the `content-event-reliability` diagnostic uses, and the two agree on which rows are *eligible* to group: both exclude rows the retention pruner has emptied (see below), so the diagnostic's duplicate counts match what this command will act on. This is a historical cleanup heuristic, not the runtime delivery identity: write-time redelivery suppression requires a stable host delivery ID plus a matching semantic fingerprint, and body equality alone never proves identity. Only `client='hook'` rows with `kind in (prompt, transcript)` participate; **command audits are never touched**. By default a group is eligible only when its members land near-simultaneously (within a 10s proximity window that clusters consecutive records pairwise, matching the diagnostic), so deliberate repeats far apart are excluded; `--strict` drops the window. The **canonical** row kept per group is the earliest parsed `created_at`, tie-broken by the smallest event id. `created_at` is parsed in Go as RFC3339Nano (never ordered lexically — `formatTimestamp` emits variable-width fractional seconds). A group containing a malformed timestamp is **skipped and reported**, never mutated.

One asymmetry remains on purpose: rows carrying a retention **ledger** entry (see below) still count toward the diagnostic's duplicate groups, because they still take part in grouping — they just can never be *archived* by this command. Eligibility (does a row group at all) and archivability (can this command remove it) are different questions; the diagnostic mirrors only eligibility.

**Responsibilities.** The CLI (`presentation/cli/store_dedupe.go`) parses flags and formats text/JSON. The usecase (`StoreManagementUsecase.DedupeContentEvents` / `RestoreContentEventDedupeRun`) mints the run id and `archived_at` timestamp on apply and validates input. The SQLite datasource (`StoreManagementDatasource`) does the transactional read/group/move and restore.

**Quarantine table.** Migration `000019` adds `event_content_dedupe_archive` (additive only — it never touches `events`). Each quarantined row preserves enough to restore the original `events` row verbatim: `id, kind, client, agent, session_id, workspace, body` (original, not normalized), `created_at, source_hook`, plus provenance `kept_event_id` (duplicate_of), `dedupe_run_id`, `archived_at`, `group_key`, and `reason`.

**Apply / restore semantics.**

- Apply commits in **bounded batches** and is **idempotent**: a second apply finds no duplicates left in `events` for an already-cleaned group, so it moves nothing.
- A batch never contains part of a duplicate cluster. Proximity clustering measures the gap between *consecutive surviving* rows, so archiving part of a cluster widens the gaps inside it and can split what was one cluster into several singletons that no re-run will collapse. Committing whole clusters means an interruption leaves every cluster either fully quarantined or untouched, and a re-run reproduces exactly what a clean run would have decided — no checkpoint state is needed. A cluster with more duplicates than one batch (1000 rows) becomes one oversized transaction rather than being split.
- Rows the retention pruner has emptied are **not eligible**. Pruning replaces the body with one fixed marker string for every row it touches, regardless of client or kind, so two prompts that were never duplicates of each other would hash to the same identity once emptied.
- Rows carrying a retention **ledger** entry are **never archived**, but they still take part in grouping. `raw_body_retention_entries.event_id` is `ON DELETE RESTRICT`, so deleting one aborts the batch. Dropping such a row from the scan instead would remove it from its identity group, and proximity clustering measures the gaps between the rows it can see: a ledger row in the middle of a cluster would widen the gap across it and split the cluster, stranding ordinary duplicates that have nothing to do with retention. The row is therefore kept as a cluster member and excluded only from the duplicates.
- Restore is **all-or-nothing** and refuses to overwrite: if any original event id already exists in `events`, the whole restore fails and nothing changes.
- Because duplicates are moved *out* of `events`, normal `list`, `search`, `doctor`, and `context` read surfaces stop showing them automatically.

**Rollback.** To undo an apply, run `traceary store dedupe content-events --restore <run-id>` (the run id is printed by `--apply` and stored on every archived row). If an apply was interrupted before it printed a run id, `--list-runs` finds it — batches commit before the id is reported, so those rows would otherwise be unreachable by both restore and purge. If you need a belt-and-braces copy, take a `traceary store backup create` before `--apply`.

**Apply failures keep their run id.** When `Apply:true` fails partway through, `StoreManagementUsecase.DedupeContentEvents` wraps the error in `apptypes.ContentEventDedupeApplyError{RunID, Err}` so the id that already-committed batches were archived under is never lost — `errors.As` recovers it. A dry-run failure never claims a run id (none was minted). `store compact`'s internal copy-filter apply (`deleteNonCanonicalDuplicateEvents`) mints its own per-execution `compact-copy-filter-<hex>` run id and wraps its failures the same way, but compact **owns** that quarantine and never ships it. On a replica/external compact the retained rollback inode (`<db>.rollback-<run>`) is the complete pre-compact store, so the committed candidate carries neither this run's archive rows nor any earlier `compact-copy-filter-*` run's; the duplicates themselves stay recoverable by content through the canonical survivor that remained in `events`. On the in-place fallback there is no rollback inode, so compact performs **no** duplicate isolation at all rather than creating quarantine that no supported command can restore (the `store dedupe content-events` CLI was removed in #1872). Operator-created quarantine is not compact's to drop: it is preserved untouched inside a 90-day window and aged out by comparing `archived_at` as an **instant**, not as RFC3339 text. `traceary doctor` reports what a store still holds under `dedupe-archive-runs`; a replica/external `traceary store compact` is what releases eligible internal runs.

**Reclaiming the bytes.** Quarantine relocates duplicates; it does not free them. Only `--purge <run-id>` drops the archived rows, and only `VACUUM` afterwards returns the freed pages to the filesystem.

**Behavior tests.** Dry-run reporting and no-mutation, apply + idempotency, restore + overwrite refusal, malformed-timestamp skip, command-audit/non-hook exclusion, retention-emptied exclusion, retention-ledger rows holding their cluster together, strict vs. proximity scope, batch-size independence, resumption after a partial apply, run listing, purge, and read-surface exclusion are covered in `infrastructure/sqlite/content_event_dedupe_test.go`; the cluster-atomicity invariant is pinned directly on the pure batch-partitioning function in `infrastructure/sqlite/content_event_dedupe_batch_internal_test.go`; flag wiring and JSON/text output in `presentation/cli/store_dedupe_test.go`; run-id minting in `application/usecase/store_dedupe_test.go`.

## Body-free workspace identity diagnostics

Migration `000023` adds `hook_delivery_attempts`. Each row records only a delivery-record ID, attempted event ID, outcome (`accepted`, `conflict`, or `exact_redelivery`), origin (`runtime` or `backfill`), and timestamp. The migration seeds one accepted/conflict attempt from every pre-existing `hook_deliveries` row but marks it `backfill`; it never copies event bodies. Release-quality rates use runtime attempts only, so seeded rows cannot make an unmeasured rollout pass. Runtime writes add the delivery and its attempt in the same transaction.

The attempted event ID is Traceary's per-callback repository identity. A later host callback gets a new event ID and therefore a new attempt row. `INSERT OR IGNORE` suppresses only an internal retry of the same event object after a transaction race, preventing repository retry mechanics from inflating the host-delivery rate.

`session_workspace_aliases` stores explicit operator review metadata. An alias never rewrites `sessions.workspace`, `events.workspace`, or an observation's ingested relationship. The read projection changes a matching stored conflict to `explicit_alias` while the review exists, so removal is a complete rollback.

`traceary doctor --json` exposes the same body-free identity fields under `workspace_identity`. The block does not run provenance catch-up. Observation volume is `SUM(observation_count)` after migration 76, which collapses `session_workspace_observations` to one row per `(session_id, workspace, observed_relationship, source_client, source_hook, observation_kind)`. `conflict_pair_count` is the distinct current-conflict `(session_id, workspace)` count, and conflict samples are one latest row per pair including `workspace`. `coverage.covered_events` / `missing_events` are catch-up frontier bookkeeping, not a per-event join. The `workspace-observations` check reports `rows` / `keys` / `orphans`; stores still on the pre-collapse shape WARN with `traceary doctor --fix` (offline class: writes refuse `Initialize` until that runs). Large-store default doctor omits the block (filesystem-metadata-only). See [workspace-conflict meaning](../research/workspace-conflict-meaning.md).

## Payload storage

Offline migration 82 decodes remaining encoded `events.body` and
`command_audits` text, then drops codec metadata. Live writers store bodies as
written plaintext (`typeof(...) = text`). There is no compact encode step and
no flag to re-enable compression.

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
