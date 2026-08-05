# Issue #1663: conservative Catalog summaries

## Structure-Behavior Design Note

### Requirement summary

- Generate deterministic format-v1 summaries from metadata columns only, using a versioned keyed HMAC, exact tokens, bounded Bloom filters, and keyed session aggregates.
- Route supported positive equality predicates through exact then Bloom evidence; every incomplete, mismatched, saturated, short, negative, or unsupported case is unknown and therefore selected.
- Reconcile only metadata-verified immutable segments already bound by the append-only Catalog ledger. Rebuild is bounded, per-segment resumable and never reads `history_units.payload`.
- Expose only aggregate coverage, storage, state, and drift diagnostics.

### Conceptual model

| Concept | State / behavior | Invariant |
|---|---|---|
| Summary generator | keyed metadata facts and fixed caps | cap transitions discard negative evidence, never facts selectively |
| Candidate evidence | exact hit, Bloom answer, or unknown | only a complete Bloom miss may exclude |
| Reconciled metadata | verified segment summary copied into derived Hot tables | epoch ledger and immutable binding remain sole authority |
| Rebuild checkpoint | durable full-ledger audit cursor plus per-segment cache cursor | retry is deterministic and idempotent; incomplete audit never reaches reconciliation |
| Diagnostics | aggregate counts only | no identity, token, digest, basename, key ID, or payload leaves the adapter |

### Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| HMAC/Bloom construction and conservative matching | `domain` | SQLite and filesystem |
| Request/result and aggregate diagnostics | `application/types` | raw metadata |
| verified manifest reconciliation and resumable derived tables | `infrastructure/sqlite` | authority reconstruction |

### Boundaries / interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `GenerateSegmentCatalogSummaryV1` | segment builder | HMAC and Bloom hashing | invalid/cap errors contain no raw values |
| `SegmentSummaryMayMatch` | later search router | exact/Bloom representation | unknown returns `true` |
| `RebuildCatalogSummaries` | maintenance/release gate | archive names, tokens and digests | ledger/binding drift fails closed |
| `CatalogSummaryDiagnostics` | operators | all identifiers | aggregate-only result |

### Behavior tests / TDD plan

Tests cover deterministic generation, exact-cap transition, Bloom saturation, key mismatch, short/negative/unsupported predicates, zero false negatives for inserted values, session aggregation, metadata-only reconciliation, interruption/resume/idempotency, ledger loss/drift, and diagnostics privacy.

### Risks / rollback

- Migration 47 is additive and contains derived data only; rollback drops its tables.
- Bloom saturation is detected before publication and degrades to unknown. Missing or malformed derived data never repairs authority from files.
- Reconciliation first completes the existing paged ledger audit, including reservation deltas, then verifies every immutable binding field, canonical manifest digest, sealed file digest, and reconstructed normalized summary before one per-segment transaction. Existing cache children are canonicalized and digest-checked again rather than trusted.
- A completed audit/cache cursor is consumed by that validation cycle, never permanent trust. A later rebuild or diagnostic restarts the full ledger audit and bounded child-cache canonicalization, so post-completion offline damage is detected.
- The format, generator, reader, and Hot schema share the v1 Bloom cap. SQL count/byte preflight and `cap+1` limits precede every derived child allocation.
- Diagnostics owns a separate durable audit/cache phase machine. Bounded pages CAS-save their cursor before returning `audit_incomplete`; only a completed cycle is restarted by the next invocation. Unreconciled bindings are ordinary coverage, while every existing parent is reconstructed into the canonical manifest digest and every child into the canonical summary digest.
- Publication and placement transitions remain outside this issue.
