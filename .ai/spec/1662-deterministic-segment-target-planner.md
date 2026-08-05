# #1662 deterministic segment target planner

## Structure-Behavior Design Note

### Requirement summary

- Select and reserve exactly one deterministic, contiguous archive target, beginning at the smallest currently Hot sequence and ending no later than the captured source high-water.
- Eligibility is fixed by a captured UTC time and Hot horizon. Capacity is fixed by row count, canonical history-unit bytes, and a versioned conservative stored-byte bound.
- A wall deadline is an invocation budget, never an input to prefix length. An incomplete scan returns `selection_incomplete` without publishing a Catalog epoch.
- Recent-first, oversize-first, malformed timestamp, concurrent append, and backdated append behavior is explicit and fail-closed.
- A measured stored/file-cap failure may shorten a released target only under a new reservation identity.

### Conceptual model

| Concept | State / behavior | Invariant |
|---|---|---|
| Planning snapshot | Expected Catalog head, captured source high-water, captured time, policy version | Same snapshot and policy yield the same target; the ordered canonical source digest is durable. |
| Candidate prefix | Consecutive canonical History Units starting at the smallest Hot sequence | No sequence is skipped; a recent or malformed unit closes/rejects at its exact position. |
| Reservation | One append-only Hot-to-reserved Catalog transition and immutable target plan/unit rows | It is committed only after the complete bounded selection scan; reserved event/sequence/audit mutation and late audit insertion are trigger-fenced. |
| Shortening proof | Released prior plan plus canonical measured stored/file cap evidence | The same failure digest must be the append-only release evidence and retry evidence; a new ID, same captured snapshot/invariant policy/source prefix, lower row cap, and strictly smaller end are required. |

### Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| Prefix/cap/time policy and canonical plan digest | `domain` | SQLite transaction and row hydration |
| Request/result/error contract | `application/types` | SQL details |
| Writer-fenced snapshot, preflight metadata sizing, incremental canonical hydration, expected-head reservation | `infrastructure/sqlite` | Segment construction/publication |

### Boundaries / interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `CatalogTargetPlanner` | #1651 migration orchestration | SQLite snapshot and Catalog commit | typed incomplete/recent/oversize/malformed/stale errors |
| `SegmentTargetPolicy` | SQLite adapter and tests | canonical framing arithmetic | deterministic target or typed outcome |

### Behavior tests / TDD plan

| Given | When | Then |
|---|---|---|
| Same rows/config at different execution speeds | plan | identical range and digest |
| Timeout/cancellation before a deterministic boundary | plan | `selection_incomplete`; no epoch/reservation |
| First row recent / oversize / malformed | plan | typed outcome; no reservation |
| Later recent / oversize | plan | preceding closed prefix is reserved |
| Concurrent/backdated append, late audit, or competing planner after snapshot | plan | writer serialization yields one reservation; append stays Hot and frozen mutation fails |
| Measured retry | shorten | canonical proof exceeds the old versioned cap; released old ID, new ID, unchanged source prefix/invariants, same start, smaller end |

### Risks / rollback

- Catalog publication is append-only; rollback is the existing append-only release delta.
- A no-op Catalog-head write acquires the SQLite writer slot before snapshot selection. Selection itself never mutates canonical rows. Any hydration inconsistency, sequence gap, head drift, or deadline prevents reservation.
- Timestamp storage class/parse validation precedes size inspection. SQLite TEXT/BLOB accounting always uses BLOB byte length, including decoded-size fallback. Each admitted row is metadata-preflighted before allocation; recent and oversize boundaries are never hydrated.
- Stored-bound v1 deliberately uses canonical plaintext length as a conservative upper bound because format v1 stores compression only when it is smaller. A later codec changes the version, not v1 semantics.
- #1651 owns actual stored/file measurement and may request a shorter retry only through the proof-bearing fields defined here.
