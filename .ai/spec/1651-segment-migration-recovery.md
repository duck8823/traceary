# Issue #1651 — segment migration and recovery

## Structure-Behavior Design Note

### Requirement summary

- Build one immutable archive segment from an already frozen #1662 reservation without changing read authority or deleting Hot rows.
- Resume by durable segment-run phase and page cursor.  Every source row is rehydrated and checked against the immutable target-plan digest before it enters the candidate.
- Publish with an install intent and a descriptor-pinned archive root, then bind the immutable file and move `Reserved -> Sealed -> VerifiedShadow` using proof-specific Catalog commits.
- Rollback is forward-only: retain any bound immutable file, append `VerifiedShadow/Sealed -> Reserved`, and return the run to a resumable terminal rollback state.  Authority remains Hot throughout #1651.
- Non-goals: `segment_authoritative`, Hot eviction, store exchange, and a generic migration framework.

### Conceptual model

| Concept | State / behavior | Invariant |
|---|---|---|
| `SegmentMigrationRun` | planned, copying, built, install-intent, installed, sealing, sealed, verifying, verified-shadow, rollback-intent, rolled-back | append-only revisions; one active run per store; reservation/range/plan immutable |
| source checkpoint | next sequence, copied rows/plain bytes, rolling digest | moves only after a committed page; exact target-plan unit digest is rechecked |
| owned candidate | private run directory and SQLite segment | never accepted by pathname alone; limits and fsync precede install |
| archive installation | durable intent plus pinned root/no-replace rename | recovery distinguishes absent, candidate-only, and installed orientations |
| Catalog proof | binding + placement epoch | binding and `Reserved -> Sealed` share one transaction; parity and `Sealed -> VerifiedShadow` share one transaction |

### Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| phase legality and immutable run identity | domain | SQL/use case |
| orchestration/resume/rollback | application use case | CLI/SQLite primitives |
| paged source hydration, journal transactions, Catalog proof commits | SQLite adapter | domain |
| segment bytes, sealing and independent verification | archive-segment infrastructure | Catalog ledger |
| OS lease, pinned-root publication, fsync | segment migration file adapter | prepared-store upgrade aggregate |

### Boundaries / interfaces

| Interface | Consumer | Error contract |
|---|---|---|
| `SegmentMigrationRepository` | use case | typed conflict, stale-source, cap, incomplete, corrupt-orientation errors |
| `SegmentMigrationUsecase` | future CLI/release gate | `Start`, bounded `Resume`, `Rollback`, `Recover` |
| proof-specific Catalog methods | migration repository | only Reserved→Sealed, Sealed→VerifiedShadow, and rollback edges |

### Behavior tests

| Given | When | Then | Level |
|---|---|---|---|
| immutable target plan | resume pages | cursor and digest advance durably without duplicate units | integration |
| cancellation after each phase | recover/resume | operation continues from the recorded boundary | integration |
| concurrent/backdated source mutation | copy/verify | reservation triggers or digest proof reject it | integration |
| installed candidate and crash | recover | pinned-root orientation is reconciled before advancing | integration |
| verified shadow | rollback | file/binding remain, Catalog returns to Reserved, Hot remains authority | integration |
| competing process | start/resume | store advisory lease serializes the run within lock/deadline caps | integration |
| disk/WAL/value/stored/file/deadline cap | resume | typed bounded failure; no authority change | integration |

### TDD plan

1. Domain transition and immutable identity tests.
2. Migration/catalog proof-edge tests and one-active-run constraint.
3. Paged exact-source copy and interruption/resume tests.
4. Publication/recovery/rollback orientation tests.
5. Full race/vet/lint validation and independent review.

### Risks / rollback

- The greatest risk is a procedural service accepting a plausible file without authenticating the frozen plan.  Every page and final digest therefore fail closed.
- Publication never overwrites an existing basename and pins the root descriptor before intent is made durable.
- Rollback does not delete a bound content-addressed file; later GC may reclaim only after Catalog reachability proof.
- #1652 exclusively owns read-authority cutover.  This issue cannot make Hot non-authoritative.

### Design checkpoint

The implementation extends the existing Catalog ledger with narrowly named proof ports and introduces a segment-specific run aggregate.  It reuses the format-v1 builder/verifier, #1662 frozen source proof, advisory-lock/fsync primitives, and does not reuse `PreparedStoreUpgradeRun`, whose exchange orientation and rollback semantics are different.
