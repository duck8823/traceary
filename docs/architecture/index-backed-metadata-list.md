# Persistent metadata projection for list and context queries

[日本語](index-backed-metadata-list.ja.md)

## Structure-Behavior Design Note

### Requirement summary

- General metadata list and context reads must retain boundary-correct
  RFC3339Nano ordering without opening body-bearing `events` records.
- Workspace/session/source-hook scopes, offset pages, and keyset pages must
  preserve their existing membership and ordering.
- The two historical source-hook body-prefix fallbacks must remain available
  without evaluating bodies at read time.
- Schema rollout must be additive, and a code rollback must leave an upgraded
  store readable and writable.

### Concepts and responsibilities

| Concept | Owner | Invariant |
| --- | --- | --- |
| Normalized timestamp | migration 000031 and event triggers | `created_at_norm, id` remains the one event-ordering contract |
| Persistent metadata projection | migration 000034 | one narrow row per event contains metadata, legacy-hook classification, and optional command-audit facts, but no body or command payload |
| Scoped metadata page | metadata datasource | list/context SQL opens only the projection and returns the existing `EventMetadata` contract |
| Transactional maintenance | event and command-audit triggers | an authoritative committed mutation and its projection cannot diverge |

### Supported plans

| Query shape | Projection ordering index |
| --- | --- |
| general metadata list, including limit/offset | `idx_event_metadata_created_at_norm_id_desc` |
| workspace list/context | `idx_event_metadata_workspace_created_at_norm_id_desc` |
| session list/context | `idx_event_metadata_session_created_at_norm_id_desc` |
| workspace + session list/context | `idx_event_metadata_workspace_session_created_at_norm_id_desc` |
| directly tagged source-hook list | `idx_event_metadata_source_hook_created_at_norm_id_desc` |

Supplied timestamp boundaries select direct-predicate SQL variants so SQLite
can seek inside the matching ordering index. Filters that are not part of the
selected scope remain post-seek predicates, preserving the public result
semantics. Legacy hook fallback scans the narrow general projection order and
compares a fixed classification populated during migration or event writes; it
does not evaluate an event body.

### Behavior tests

1. Upgrade only a private copy of a pre-000034 store; require an unchanged
   source digest, equal event/projection counts, `integrity_check=ok`, and zero
   foreign-key violations.
2. Verify existing-row backfill and transactional event/audit
   insert/update/delete maintenance.
3. Assert representative list/context SQL and plans use the projection, never
   open `events`, and do not build a temporary order-by tree.
4. Preserve timestamp order, filters, failures-only semantics, legacy hook
   membership, offset paging, and composite keyset paging.
5. Keep the 10k-event direct-range p95 below 50 ms as a CI smoke test.
6. Exercise an external write lock, require a busy/locked result with no partial
   projection object, then retry successfully after releasing the lock.
7. Open an upgraded store through the pre-projection migration set and verify
   its subsequent event write is maintained by the persisted triggers.
8. Run both opt-in operational benchmarks before release. Their durable
   summaries contain numeric metrics and fixed booleans only.

### Performance and migration evidence

The CI smoke measurement on 2026-07-26 used Go 1.26.3 on macOS 26.5
(darwin/arm64, Apple M4) with the modernc SQLite driver. It contained 10,000
projected event rows; 25 workspace-scoped two-second direct ranges with
`limit=50` measured p95 **412.25 µs** against the 50 ms target.

The copied-store migration benchmark creates a private synthetic 256 MiB body
extent, copies the pre-000034 source, and migrates only the copy:

```sh
TRACEARY_RUN_METADATA_PROJECTION_MIGRATION_BENCHMARK=1 \
  go test -v ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkEventMetadataProjectionCopiedStoreMigration -benchtime=1x
```

On the same host, migration 34 completed in **309.6 ms** for 8 events. Source,
copy-before, and copy-after sizes were each 302,714,880 bytes; measured main-file
growth was 0 bytes because existing free pages held the narrow projection.
Peak scratch extent was 605,528,480 bytes and post-checkpoint scratch was
605,429,760 bytes. Integrity passed, foreign-key violations were zero, and the
source stayed byte-identical. An externally held write lock returned the
expected busy outcome after **1,012.467 ms**, exposed zero partial projection
objects, and the retry succeeded after release.

The Phase-A benchmark creates eight SQLite-generated 256 MiB extents (at least
2 GiB managed and stored body bytes), verifies equal event/projection counts,
requires a projection-only ordered plan, and measures 25 direct metadata
ranges:

```sh
TRACEARY_RUN_MULTI_GIB_BENCHMARK=1 \
  go test -v ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkMetadataDirectRangeMultiGiB -benchtime=1x
```

It reports managed/stored bytes, event/projection counts, missing body metadata,
returned body bytes, plan classification, run count, and p95. The p95 gate is
below 250 ms. The fixture lives only in the Go test temporary directory and is
removed after the benchmark; CI never creates it.

On the same host, Phase A measured 2,418,753,536 managed bytes and
2,147,483,648 stored body bytes across 8 event/projection rows. All 25
measurements used the projection-only plan, missing body metadata and returned
body bytes were both zero, and p95 was **0.2921 ms**, passing the 250 ms gate.

### Rollback and residual risk

Migration 000034 is additive. Older binaries ignore its table and indexes while
the persisted triggers continue to maintain projection rows for their writes.
After release, application code can therefore roll back to authoritative-table
reads without removing schema objects. Removing the projection requires a later
forward migration after no deployed reader depends on it.

Backfill and index creation execute in one migration transaction. Large stores
therefore need temporary disk capacity and can make competing writers wait up
to the configured busy timeout. Any migration failure rolls back the table,
indexes, triggers, and migration record together. The projection adds one
narrow row and five ordering indexes per event, so deployments must still
monitor write latency and WAL/checkpoint growth.
