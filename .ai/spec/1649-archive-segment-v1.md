# #1649 archive segment format v1

## Structure-Behavior Design Note

### Requirement summary

- Preserve one event and its optional audit as an indivisible History Unit.
- Encode SQLite values canonically without collapsing NULL, INTEGER, REAL, TEXT, or BLOB.
- Build and independently verify bounded, immutable, zstd-compressed SQLite segment files.
- Do not implement Catalog state, Hot migration, publication, cutover, deletion, or CLI behavior.

### Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| History Unit | fixed 23-column event v1 plus optional fixed 27-column audit v1 | derives identity and time from the event row | `event_id` is absent from audit input and is encoded once, so an audit cannot name another parent |
| Canonical value | SQLite storage class and bytes/value | length-delimited v1 encoding | NULL, TEXT, and BLOB remain distinguishable |
| Segment identity | store, format, closed sequence range, logical digest | produces a content-addressed basename | every identity component is digest-bound |
| Candidate | writable SQLite file | accumulates bounded encoded units | it is never reported as sealed after an error |
| Sealed segment | read-only format-v1 SQLite file | supports manifest-only and full verification | Hot migrations never run against it |

### Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| Canonical values, History Unit, identity invariants | `domain` | filesystem and SQLite |
| Bounded codec, file layout, construction and verification | `infrastructure/sqlite` | Catalog/publication orchestration |
| Catalog, placement, migration journal | later issues | this format implementation |

### Boundaries / interfaces

The builder accepts already ordered History Units and explicit resource caps in a caller-owned candidate directory pinned with `os.Root`; it performs no archive publication. The verifier accepts an archive root plus a durable expected manifest, pins the directory and leaf with `openat`/`O_NOFOLLOW`, preflights SQLite payload lengths before loading BLOBs, and verifies the expected file digest and logical identity before accepting it. Manifest-only inspection is bounded but untrusted structural evidence; it does not hash or decode payloads. Typed sentinel errors distinguish unsupported format/codec, corruption, cap exhaustion, and unsafe location.

### Behavior tests and TDD plan

Tests first cover canonical mixed values, identity determinism, zstd/plain boundaries, corruption, caps, cancellation, unsafe locations, symlinks, unknown format/codec, manifest-only inspection, file mode, and incomplete candidate cleanup. Green uses small explicit v1 types; no generic archive framework is introduced.

### Risks / rollback

- SQLite files are not required to be byte-deterministic; logical bytes and manifest logical fields are.
- The format is additive and not activated. Rollback is removal of these unused types/files.
- No-replace publication and directory fsync intentionally remain #1651 responsibilities.
