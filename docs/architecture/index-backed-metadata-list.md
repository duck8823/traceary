# Index-backed metadata list and context queries

[日本語](index-backed-metadata-list.ja.md)

## Structure-Behavior Design Note

### Requirement summary

- General metadata list and context reads must retain boundary-correct
  RFC3339Nano ordering without a whole-store temporary sort.
- Workspace/session scopes and page offsets must remain metadata-only.
- Schema rollout must be additive; a code rollback must not make an existing
  store unreadable.

### Conceptual model and responsibilities

| Concept | Owner | Invariant |
| --- | --- | --- |
| Normalized timestamp order | persisted event timestamp and SQLite indexes | `created_at_norm, id` is the one ordering contract |
| Scoped metadata page | metadata datasource | selects metadata/audit columns, never `events.body` |
| Index rollout | migration 000031 | adds and backfills `created_at_norm`, then maintains it with triggers |

### Boundaries and supported plans

| Query shape | Ordered index |
| --- | --- |
| general metadata list (including limit/offset) | `idx_events_created_at_norm_id_desc` |
| workspace list/context | `idx_events_workspace_created_at_norm_id_desc` |
| session list/context | `idx_events_session_created_at_norm_id_desc` |
| workspace + session context | `idx_events_workspace_session_created_at_norm_id_desc` |
| source-hook list | `idx_events_source_hook_created_at_norm_id_desc` |

The list predicates remain optional for public filters, but supplied timestamp
bounds select direct SQL variants so the planner can seek within the matching
ordering index. The planner can scan the matching ordering index;
for an unfiltered scoped page it stops after `offset + limit`, while filters
that are not an indexed scope are applied during that ordered scan. This is
preferable to duplicating every filter combination or changing result
membership.

### Behavior tests and TDD plan

1. Assert representative list/context and legacy source-hook fallback
   `EXPLAIN QUERY PLAN` output from a migrated store uses the
   documented ordering index and does not build a temporary order-by tree.
2. Assert normal list and scoped/offset pages preserve timestamp order.
3. Keep the metadata SQL select-list guard: `body` and command payload columns
   are forbidden in every metadata query.
4. Upgrade a pre-000031 store and assert the fixed-width timestamp is
   backfilled and maintained after inserts and timestamp updates.
5. Keep the 10k-event direct-range p95 below 50 ms as a CI smoke test. The
   migrated-store plan test also requires direct lower and upper timestamp
   constraints and rejects every temporary B-tree, including partial order-by
   sorts.
6. Run the opt-in multi-GiB benchmark before release. It writes actual SQLite
   pages and event bodies under a temporary directory, verifies both
   `page_count * page_size` and `SUM(length(body))`, and is never run by CI.

### Performance evidence

The CI smoke measurement on 2026-07-25 used Go 1.26.3 on macOS 26.5
(darwin/arm64) with the modernc SQLite driver. It contains 10,000 indexed event
metadata rows; 25 workspace-scoped, two-second direct ranges with `limit=50`
measured p95 **416.125us** against a 50 ms target.

The release-QA benchmark is opt-in and creates 8 events with 256 MiB bodies
(at least 2 GiB total), then verifies `page_count * page_size >= 2 GiB`, the
event count, and the stored body total before measuring 25 direct ranges. Run:

```sh
TRACEARY_RUN_MULTI_GIB_BENCHMARK=1 \
  go test ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkMetadataDirectRangeMultiGiB -benchtime=1x
```

Its p95 goal is below 250 ms. A 2026-07-25 attempt in this constrained runner
reached benchmark setup but was terminated before fixture completion, so it has
no valid multi-GiB p95 to report. The runner had 7.8 GiB free but does not allow
the required long-lived benchmark process; release QA must run the command on a
host that permits it and record the p95 before release. The command creates its
artifact only in the Go test temporary directory and deletes it afterwards; no
large fixture is committed or run by CI. The direct-range plan assertion is the
primary full-scan regression guard, while the 10k smoke threshold detects local
latency regressions.

### Rollback and residual risk

Migration 000031 is additive and idempotent. Reverting application code keeps
the store readable because older readers ignore the added column, triggers, and
indexes. Index creation can temporarily require disk and write-lock
capacity on very large stores; operators should retry after freeing capacity or
quiescing writers.
