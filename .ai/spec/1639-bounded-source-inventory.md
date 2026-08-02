# #1639 bounded historical source inventory

## Structure-Behavior Design Note

### Requirement summary

- Migration 38 must create projection schema without scanning or inserting every historical event.
- A projection generation explicitly inventories historical event identities in resumable batches before projecting payloads.
- Every inventory batch is capped by rows, identity bytes, wall time, and write-lock time; cursor and inserted identities commit atomically.
- Cancellation rolls back the current batch. A fresh process resumes from the durable cursor. Canonical mutation invalidates an in-progress inventory.
- Existing databases whose source sequence is already populated remain compatible. Payload rehearsal limits, WAL controls, and preflight checks are unchanged.

### Conceptual model

| Concept | State | Behavior | Constraint / invariant |
|---|---|---|---|
| Historical inventory | generation, last event ID, source revision | copies a bounded keyset of missing canonical identities | identity rows and cursor commit together |
| Inventory batch | ordered event IDs and byte ledger | admits a prefix and persists exactly that prefix | rows/bytes/wall/lock never exceed configured caps |
| Projection source | monotonic source sequence | freezes high-water only after inventory is complete | payload projection never starts with missing historical identities |
| Drift fence | canonical source revision | invalidates inventory on event/audit mutation | a fresh generation is required after drift |

### Responsibility assignment

| Responsibility | Owner | Reason to change | Not owner / reason |
|---|---|---|---|
| Schema-only upgrade and mutation triggers | migration 38 | schema compatibility | migration runner must not perform data work |
| Inventory admission and state transition | application use case/types | cap and lifecycle policy | CLI only supplies reviewed budgets |
| Bounded keyset selection and atomic persistence | SQLite adapter | SQL, transaction, cursor | planner does not depend on SQLite DTOs |

### Boundaries / interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `SelectInventory` | projection use case | missing-row query and keyset | context/cap/SQL errors, no state change |
| `ApplyInventoryBatch` | projection use case | transaction and sequence assignment | cancellation rolls back; CAS/drift returns typed drift |
| existing `SelectSnapshot` | projection use case | payload codec hydration | callable only after inventory phase |

### Behavior tests

| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| schema-only migration | a large pre-38 event table | apply migration 38 | completes under one second and source sequence is empty | SQLite regression |
| bounded fresh resume | historical rows exceed one batch | resume from a new use case instance | cursor advances atomically and all rows are inventoried once | SQLite integration |
| cancellation rollback | cancellation during inventory apply | resume | no partial cursor/identity commit | SQLite integration |
| drift invalidation | canonical mutation during inventory | resume/apply | typed drift and no stale projection start | SQLite integration |
| populated compatibility | source sequence already contains all events | resume | inventory completes without duplicate rows and projection begins | SQLite integration |

### TDD plan

1. Add migration timing/schema test and make migration 38 data-free.
2. Add inventory types/ports and use-case phase routing.
3. Implement bounded SQLite selection and atomic apply/CAS.
4. Add cancellation, fresh-resume, drift, and populated-store tests.
5. Run full tests and lint; self-review cap and transaction paths.

### Risks / rollback

- Procedural risk: inventory is a distinct lifecycle phase rather than another branch inside payload hydration.
- Migration compatibility: migration 38 remains additive; this release is unreleased, so its state check and cursor column are corrected in place. Already-populated development stores skip missing identities safely.
- Rollback: stop the generation; canonical events/audits are never changed. Derived inventory rows can be retained and reused by a fresh generation.
- Split: schema-only change, lifecycle implementation, and operational documentation are one issue because their compatibility invariant is tested end-to-end.
- Design checkpoint: use stable event-ID keyset rather than SQLite rowid so `VACUUM` cannot invalidate the cursor silently.
