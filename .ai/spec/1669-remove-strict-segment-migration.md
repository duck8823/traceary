# #1669 reduce hot/cold to the minimal segment foundation

## Structure-Behavior Design Note

### Requirement summary
- Purpose: reduce the v0.34.0 hot/cold implementation to the smallest maintainable subset before #1652/#1653.
- Pass 1 (already on this branch): remove the strict #1651 migration workflow, its run journal, rollback state machine, and dedicated schema/tests.
- Pass 2 (this note): remove every remaining component that only served the abandoned strict guarantees, and keep exactly:
  1. the immutable compressed segment format with a small manifest (#1649, without embedded search summaries);
  2. a lightweight segment catalog table sufficient to enumerate registered segment files.
- Accepted by decision: at-least-once archive processing, Hot/segment physical overlap, segment-granularity restart, temporarily partial historical search, and no compatibility with unreleased development databases that applied migrations 44-47.
- Non-goal: implement the #1652 search router, the #1653 evictor, or any archiver that assigns archive sequences. Those own their storage when they land.

### Removal decision table
| Component (merged by) | Decision | Reason |
|---|---|---|
| Segment codec, identity, builder, verifier (#1649) | Keep, simplified | Directly required by minimal component 1. |
| Embedded segment summaries: HMAC tokens, Blooms, session aggregates (#1658/#1663) | Remove | Literal search over selected segments needs no probabilistic prefilter; manifest bounds are enough. |
| Catalog ledger: epochs, transitions, reservations, placement states, digest chain (#1650/#1661) | Remove | Authority tracking for the abandoned strict cutover. |
| Archive sequence inventory, allocator, lineage, insert trigger, write-error mapping (#1660) | Remove | Sequence assignment is an archiver concern; nothing retained consumes sequences at insert time. |
| Deterministic durable target planner (#1662) | Remove | Durable target plans belong to the discarded exactly-once workflow. |
| Catalog summary reconciliation generations (#1663) | Remove | Serves removed summaries. |
| Migrations 44-47 | Remove | All tables serve removed machinery. Replaced by one minimal catalog migration 44. |
| Specs 1647/1658/1660/1661/1662/1663 | Remove | Document discarded guarantees only. |

### Conceptual model
| Concept | Behavior | Invariant |
|---|---|---|
| Segment | Immutable compressed copy of one contiguous sequence range, self-describing via an embedded manifest | Sealed read-only; content-addressed basename binds store, range, and logical digest. |
| Manifest | Small aggregate evidence: range, counts, byte totals, time bounds, digests | `time_complete` is false whenever any unit lacks a valid timestamp, so selection never claims false completeness. |
| Catalog row | One registered manifest in Hot | Inserted only after the segment file is durably sealed and verified; rows never carry Hot authority or workflow state. |
| Overlap | A record may exist in Hot and in one or more segments | Future readers deduplicate by stable event ID; the catalog permits overlapping ranges. |

### Responsibility assignment
| Responsibility | Owner | Not owner |
|---|---|---|
| Canonical unit encoding, segment identity | `domain/archive_segment.go` | filesystem, SQLite |
| Segment file build/inspect/verify | `infrastructure/sqlite/archive_segment.go` | catalog registration, archive orchestration |
| Catalog registration/enumeration, install verification and directory durability at registration | `infrastructure/sqlite/archive_segment_catalog.go` | segment selection policy, eviction, sequence assignment |
| Hot operational reads (active/latest/handoff) | existing query services | archive segments |

### Boundaries / interfaces
- `RegisterArchiveSegment(ctx, root, manifest, limits)` is the only registration operation and composes verification with insertion: it re-verifies the installed sealed file under `root` (safe root/basename, sealed read-only mode, manifest equality including the file digest, full payload decode), fsyncs the archive root directory so the installation is durable, and only then inserts the catalog row. No raw manifest-insert API is exposed, so a catalog row cannot claim coverage for a nonexistent, unsealed, or unverified file. Registration is idempotent for an identical manifest and fails on a same-basename contradiction without mutating the stored row.
- `ListArchiveSegments(ctx)` returns every registered manifest ordered by range; callers select.
- Normal store initialization (`Database.migrate`) validates every applied `schema_migrations` entry at or below the catalog's maximum version: the version must exist in the catalog and its recorded name must match. This fails closed on development stores written by the abandoned catalog instead of silently skipping the replacement migration 44; because migrations apply in version order, every store that applied old 45-47 also recorded old 44 under its abandoned name. Applied versions above the catalog maximum stay tolerated so a store upgraded by a newer release remains openable after a binary rollback (existing behavior, gated by `store_format_state`), and released gap histories keep applying missing versions as before. The prepared-migration path keeps its stricter exact-prefix check. No compatibility migration or framework is added.
- No application-layer port is declared: nothing consumes the catalog yet, and #1652 defines the port it actually needs.

### Behavior tests
| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| Retained segment behavior | summaries removed | full suite runs | build/inspect/verify tests still pass | infrastructure |
| Time completeness survives in the manifest | a unit with a malformed timestamp | a segment is built | manifest reports `time_complete=false` | infrastructure |
| Catalog roundtrip | a sealed installed segment | it is registered twice | one row; list returns the identical manifest | infrastructure |
| Catalog contradiction | a registered basename | a same-basename rebuild with differing sealed evidence registers | registration fails, row unchanged | infrastructure |
| Unverified rejection | a manifest with a missing/invented file digest or mismatched identity | registration is attempted | rejected; no row | infrastructure |
| Installation required | the sealed file is unsealed (0600) or deleted | registration is attempted | rejected; no row | infrastructure |
| Fresh schema | an empty database | initialization runs | migrations 1-44 apply; no removed table exists | integration |
| Abandoned-catalog ledger | a store that applied old migrations 44-45 | normal initialization runs | initialization fails on the renamed version 44; new migration 44 is not silently skipped | integration |
| Released gap history | a store missing one released migration version | normal initialization runs | the missing version is applied as before | integration |
| Newer-store rollback | a store upgraded by a newer catalog | an older catalog initializes | applied versions above the catalog maximum stay tolerated (existing rollback test) | integration |

### TDD plan
1. Delete discarded features as whole files plus exact baseline reversion of their edits inside retained files.
2. Simplify the segment builder/verifier by removing the summary block; adapt existing tests, keeping the behavior matrix (corruption, caps, cancellation, unsafe locations, sealing).
3. Add migration 44 and the catalog datasource test-first against the behaviors above.
4. Run the full suite and lint.

### Risks / rollback
- Removed sequence assignment means a future archiver must define its own stable order before building segments; the segment format itself only requires contiguous caller-supplied sequences.
- Development databases that applied old migrations 44-47 fail the normal-initialize ledger compatibility check (unknown version or renamed version 44) and the prepared-path exact-prefix check. Accepted: v0.34.0 is unreleased; recreate the store.
- The logical digest and manifest schema change (summary frame removed). Accepted for the same reason; `SegmentFormatV1` stays 1 because no sealed segment exists outside tests.
- Rollback: revert this cleanup PR before merge. No live database mutation is performed.
