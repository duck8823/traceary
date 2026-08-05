# Hot/cold segmented history architecture

[日本語](hot-cold-segmented-history.ja.md)

Status: design checkpoint for #1648. Implementation is split across #1649–#1654.

## Decision

Traceary will keep every canonical event and command audit while bounding the writable Hot SQLite database. Recent records and latency-sensitive views stay in Hot. Older records move into immutable, compressed SQLite segment files. A lightweight Segment Catalog in Hot records placement and summary metadata. Federated readers pin a Catalog epoch, query Hot and only the selected segments, and merge the results without duplicates.

The alternatives were (a) immutable SQLite segments, (b) a custom append-only bundle, and (c) one Cold SQLite database. We select (a): it reuses SQLite validation and query behavior without creating a new storage engine. A custom bundle adds codec and recovery risk. A single Cold database recreates the unbounded database and full-history resume problem.

This design does not make a full-history search projection or a 45-minute generation run a v0.34.0 release condition. Existing search remains available during staged migration.

## Requirements and non-goals

- Preserve all canonical `events` and `command_audits`; compression changes representation, not retention.
- Keep active/latest/handoff and recent-detail latency on the Hot path.
- Resume and verify one segment at a time.
- Reuse prepared offline candidates, advisory locks, atomic exchange, durable recovery, migration catalog, resource caps, and source-invariance checks.
- Do not turn the copied-store release-gate runner into a general operational framework.
- Do not use legacy archive, GC, or retention deletion as a second authority for canonical history.

## Conceptual model and invariants

| Concept | State / behavior | Invariant |
|---|---|---|
| History unit | An event and its optional command audit, with one monotonically assigned event archive sequence | The audit follows its parent event; a unit has one authoritative placement. |
| Segment | Closed sequence interval plus compressed payload, indexes, and manifest | Once sealed, its bytes and logical contents never change. |
| Catalog epoch | Immutable view of segment metadata and placement | A read pins one epoch; publication is atomic. |
| Migration run | Copies, verifies, publishes, then optionally evicts one segment | Its durable journal resumes at an idempotent boundary. |
| Authority | Hot before publication; segment after placement publication | Duplicate physical copies never produce duplicate logical results. |
| Legacy compatibility | Existing Hot projection remains available for records not cut over | Removal follows verified segment coverage; it never precedes it. |

Archive sequence is assigned transactionally by a trigger when a new event is inserted. #1650 first creates a stable store UUID/lineage and then inventories existing events with a bounded, resumable backfill. Activation is forbidden until the inventory is complete, gap-free, and unique. Its optional command audit belongs to the same History Unit and follows the parent event into the same segment. A migration captures a high-water event sequence. Later or backdated events receive greater sequences and enter a later segment; segment time ranges may therefore overlap. Existing units in a building or frozen range reject event/audit update and delete. v0.34 does not support mutation of a cold unit; a future correction must be an append-only, versioned tombstone/correction record rather than rewriting a sealed segment.

A target starts at the smallest unplaced sequence and is the largest contiguous closed prefix satisfying the Hot time horizon plus configured maximum rows, uncompressed bytes, stored bytes, and wall time. It never skips a sequence to make a larger segment.

Segment identity is `store_id + format version + start sequence + end sequence + logical digest`. The manifest and Catalog bind all five values. Location is a content-addressed basename relative to the configured archive root; absolute paths, traversal, and symlinks are rejected. The sealed schema remains at its segment format version and never receives Hot database migrations. The logical digest is computed over a versioned canonical record encoding; a separate file digest detects physical corruption. This avoids assuming that equivalent SQLite files are byte-identical.

## Responsibilities and boundaries

| Responsibility | Owner | Boundary |
|---|---|---|
| Canonical record and sequence invariants | `domain` | No SQLite/file details. |
| Segment construction and migration orchestration | `application/usecase` | Ports for candidate building, journal, publication, and verification. |
| Catalog snapshot and federated query | `application/queryservice` | Consumer-oriented Hot/segment reader interfaces. |
| SQLite schema, segment files, locks, exchange, codecs | `infrastructure/sqlite` | Maps storage failures to typed application errors. |
| Explicit maintenance commands and diagnostics | `presentation/cli` | No hidden migration on ordinary reads. |

The Hot store and its adjacent archive directory form one logical store. Backup/restore must capture the Hot database, a pinned Catalog snapshot, and every referenced segment manifest/file. A SQLite-only backup is incomplete after the first segment is published.

## Segment and Catalog contract

A sealed segment contains complete History Units for one bounded event-sequence interval, minimal lookup/order indexes, and a segment-local search representation. Segment format v1 uses zstd for large text/blob values. Its length-delimited canonical digest encoding preserves SQLite storage class, NULL, and raw bytes; decoded values must equal the originals. Its manifest records format/catalog versions, interval, counts, logical/file digests, codec facts, creation provenance, and aggregate validation results. It contains no machine-specific source path. The existing JSON/gzip `StoreArchiver`, which materializes whole tables as `[]map[string]any`, is not reused for segment construction.

The Catalog stores interval, time bounds, counts, state, location, digests, format version, and bounded candidate-search summaries. It must not duplicate raw bodies. No-false-negative filters are time overlap; exact or Bloom membership for workspace, session, client, agent, and kind; and a keyed literal n-gram Bloom. Non-filterable predicates select every time-overlapping segment. Candidate structures may return false positives but not false negatives. Exact filtering always occurs against Hot or the selected segment.

Catalog state transitions are:

`building -> sealed -> verified_shadow -> segment_authoritative -> evicting -> cold`

Through `verified_shadow`, Hot alone is authoritative and the segment is parity-only. After parity, one short Catalog transaction creates a new epoch and changes the interval to `segment_authoritative`; readers never treat both physical copies as authorities. Hot duplicates remain only as rollback material. `evicting` starts only after rollback evidence permits deletion, and `cold` follows verified reclaim. Catalog placement state is separate from the external journal phase; recovery ends explicitly in `abandoned` or `rolled_back` when it does not resume forward.

File publication and the Hot Catalog transaction cannot be one filesystem/SQLite atomic operation. The publisher fsyncs the segment, installs it by content-addressed no-replace rename, fsyncs the directory, records durable intent, and only then commits a Catalog transaction. A crash before the Catalog commit leaves a detectable orphan. A crash afterward is reconciled from the journal plus observed Catalog/file state. A Catalog transaction must never reference an unverified or missing file.

## Federated reads and search

A reader starts a Hot read transaction, pins the current Catalog epoch, opens the exact immutable segment versions referenced by that epoch, and queries sources in parallel or bounded sequence. Results merge by the existing contract `created_at_norm DESC, event_id DESC`. The plan excludes Hot rows in segment-authoritative intervals; post-hoc ID deduplication is defense-in-depth, not authority selection. A continuation authenticates the query hash, Catalog epoch, source set, and each source anchor. v0.34 epochs are append-only and their referenced segments are not deleted while an epoch can be resumed.

Sessions remain canonical in Hot. Each segment contributes a session aggregate; it combines without duplication with a transactionally updated Hot delta for late events. A Hot session-resume projection retains active lifecycle, latest metadata, a bounded recent-command preview, and the latest compact summary. v0.34 introduces one explicit public maximum for CLI `--recent` and MCP context-pack criteria; the projection retains exactly that maximum, over-limit requests return a typed validation error, and existing default output is unchanged. Active/latest/handoff use that Hot projection and must succeed even when an unrelated cold segment is unavailable. Historical search first applies workspace/session/time/type and safe Catalog candidate filters, then opens only candidate segments. Unsupported or nonselective predicates may scan bounded segments but never trigger a persistent full-history generation.

Missing or corrupt required segments produce an explicit partial/unavailable error with aggregate diagnostics; Traceary must not silently claim a complete result.

## Migration, rollback, and reclaim

Each migration run owns exactly one target interval:

1. Briefly pin source identity, Catalog epoch, target range, and high-water sequence under the exclusive store advisory lock.
2. Release the exclusive lease and copy into an owned offline candidate with disk/time/row/write/decoded-byte caps; before seal, briefly freeze and verify the frozen range identity/logical digest rather than a whole-file SHA.
3. Checkpoint, fsync, seal, and verify counts, logical digests, references, decode parity, and source invariance; the result is `verified_shadow` and Hot remains the sole authority.
4. Install the file, reconcile durable intent, run router parity, then use a short Catalog transaction to cut authority over to the segment.
5. Delete only identities proven segment-authoritative in that exact interval.
7. Rebuild/compact an offline Hot candidate, atomically exchange it, and retain rollback evidence.

Before eviction, rollback publishes a new epoch placing the interval back in Hot and removes only an unreferenced candidate. After eviction, rollback reconstructs an offline Hot candidate from the sealed segment, verifies it, atomically exchanges Hot, then publishes the returning placement epoch. A process crash at every durable boundary must resume idempotently. Non-cooperating old processes and old binaries must be stopped before apply/resume/rollback; direct SQLite compatibility after eviction is not promised.

## Existing maintenance surface

| Surface | v0.34 rule |
|---|---|
| Event/audit GC, archive `--delete-after-verify`, raw-body retention, dedupe apply/restore | Fail closed with a typed conflict if selected IDs are frozen or segment-authoritative. Only an eviction capability bound to an exact segment ID and Catalog epoch may delete canonical units. |
| Backup/restore | A SQLite-only backup is explicitly incomplete and is rejected where complete-history backup is requested. |
| Compact | Allowed only at a stable Catalog epoch with no active migration run. |
| Legacy search maintenance | Maintains the Hot compatibility projection only and cannot change segment authority. |

#1652 introduces a Federated read facade and swaps composition-root read ports; it does not add segment orchestration to the Hot-writing `EventDatasource`.

## Observable behavior tests and release gate

| Given | When | Then |
|---|---|---|
| Concurrent writes after captured high-water | one interval migrates | new records stay Hot and no captured record is lost or duplicated. |
| Arbitrary bytes in event/audit fields | segment decode runs | decoded bytes and logical digest match the source. |
| Crash before/after file and Catalog publication | resume runs | exactly one authoritative placement is recovered. |
| Dual physical copies | federated list/search runs | each canonical identity appears once in stable order. |
| Selective historical query | router plans it | only Catalog-selected segments open, with no false negatives. |
| Segment unavailable/corrupt | a complete query needs it | a typed incomplete result is returned. |
| An unrelated cold segment is unavailable | active/latest/handoff runs | the Hot session-resume projection still returns a complete result. |
| Verified segment is evicted and Hot reclaimed | rollback runs | history is restored and source invariants hold. |
| Legacy and federated readers during rollout | covered ranges increase | observable results stay compatible. |

The v0.34.0 release gate uses only a reviewed copy, publishes at least one segment, exercises crash/resume, performs real eviction and physical reclaim, proves before/after parity for the existing CLI/MCP list, search, session, active/latest/handoff surfaces through the new router, proves rollback, and emits aggregate-only evidence. It does not require full-history projection completion. Compatibility means those supported surfaces, not an old binary or direct SQLite reader after eviction.

## Delivery split

- #1649 defines/builds format v1 and its codec/validation without activation.
- #1650 owns schema-only store lineage, bounded sequence inventory, Catalog, epochs, and placement invariants.
- #1651 owns shadow construction, file publication, journal, and recovery without authority cutover.
- #1652 owns federated reads/search, parity, stable pagination, and authority cutover without deletion.
- #1653 owns eviction, physical reclaim, and post-eviction rollback.
- #1654 owns CLI operations, complete backup/restore, diagnostics, and bilingual docs.
- #1620 owns same-head segment-gate evidence; #1621 owns release preparation.

Schema migrations are allocated serially by the implementing issue from the then-current shared migration catalog; issue numbers do not pre-reserve migration numbers. Every implementation starts from current `main`, adds behavior tests first, and must not copy the abandoned full-history runner.

## Risks and rollback triggers

- Catalog false negatives, mixed-epoch reads, digest mismatch, missing segments, or an unproven source mutation block publication/eviction.
- Capacity estimates must include candidate, WAL, segment, rollback, and exchange headroom.
- Compression/encryption/privacy policy is versioned per segment; logs and public evidence remain aggregate-only.
- The first v0.34.0 rollout retains Hot duplicates until segment reads pass. Destructive eviction is limited to the release-gate copy until all preceding invariants are proven.
