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

The list predicates remain deliberately optional so all public filters keep
their existing semantics. The planner can scan the matching ordering index;
for an unfiltered scoped page it stops after `offset + limit`, while filters
that are not an indexed scope are applied during that ordered scan. This is
preferable to duplicating every filter combination or changing result
membership.

### Behavior tests and TDD plan

1. Assert representative list/context `EXPLAIN QUERY PLAN` output uses the
   documented ordering index and does not build a temporary order-by tree.
2. Assert normal list and scoped/offset pages preserve timestamp order.
3. Keep the metadata SQL select-list guard: `body` and command payload columns
   are forbidden in every metadata query.
4. Upgrade a pre-000031 store and assert the fixed-width timestamp is
   backfilled and maintained after inserts and timestamp updates.

### Rollback and residual risk

Migration 000031 is additive and idempotent. Reverting application code keeps
the store readable because older readers ignore the added column, triggers, and
replacement indexes. Index creation can temporarily require disk and write-lock
capacity on very large stores; operators should retry after freeing capacity or
quiescing writers. The p95 target is guarded structurally by index plans rather
than a machine-dependent wall-clock benchmark.
