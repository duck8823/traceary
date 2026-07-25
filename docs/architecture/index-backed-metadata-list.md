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
5. Exercise a migrated, body-free 10k-event fixture with a sparse 4 GiB file
   extent. The direct workspace-range query is measured 25 times; CI requires
   p95 below 50 ms, while the migrated-store plan test requires direct lower
   and upper timestamp constraints and rejects every temporary order-by tree.

### Performance evidence

Measured on 2026-07-25 with Go 1.26.3 on macOS 26.5 (darwin/arm64), using the
modernc SQLite driver. The fixture contains 10,000 indexed event metadata rows
and a sparse 4 GiB database-file extent; it deliberately contains no large body
corpus and is created only under the test temporary directory. The workload is
25 repetitions of a workspace-scoped, two-second direct range with `limit=50`.
The goal is p95 below 50 ms; the measured p95 was **416.125us**. The structural
plan assertion is the primary full-scan regression guard, and the p95 threshold
is the CI smoke guard. No generated fixture is committed.

### Rollback and residual risk

Migration 000031 is additive and idempotent. Reverting application code keeps
the store readable because older readers ignore the added column, triggers, and
indexes. Index creation can temporarily require disk and write-lock
capacity on very large stores; operators should retry after freeing capacity or
quiescing writers.
