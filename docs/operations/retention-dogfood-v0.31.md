# v0.31 retention copied-store dogfood

[日本語](./retention-dogfood-v0.31.ja.md)

Status: Issue #1445 dogfood completed on 2026-07-22; release-blocking follow-up #1486 is tracked below and must close before release. All paths below were disposable copies or synthetic roots under `/private/tmp`; no command targeted the only copy of a live store.

## Decision

- Raw-body prune/restore and archive/backup capacity plan/apply are public CLI commands in v0.31, but remain manual and opt-in.
- Install, update, hooks, `doctor`, and normal reads never create or apply a retention plan.
- Automatic archive-before-GC remains separately opt-in through `retention.mode=archive_then_gc`; its default is `disabled`.
- SQLite compaction remains a separate operation and was not run by either retention apply.
- Metadata and aggregates are not physically deleted by the v0.31 retention executor.

## Raw-body drill

The disposable database contained one 56-byte body and one event metadata row. A verified, unencrypted archive covering that event was created before planning.

| Observation | Before | After apply | After restore |
|---|---:|---:|---:|
| body logical bytes | 56 | unavailable (`body_unavailable_reason=retention`) | 56 |
| SQLite logical file size | 262,144 | 262,144 | 262,144 |
| SQLite allocated bytes | 262,144 | 262,144 | 262,144 |
| event/session/source metadata | baseline | byte-equivalent except the documented retention lifecycle fields | original values |
| `PRAGMA integrity_check` | `ok` | `ok` through apply tests | `ok` |

Evidence:

- Plan `3ba7a0ef48b0fffc89258281ac265d4dbb60f80ac291629074e78a9f0409185d` selected the exact event identity for the `age` reason and pinned recovery digest `927d130e00e98586fb5caace14803f7170df1df82c10a593715bef895fd63db1`.
- A wrong confirmation failed and left the database SHA-256 unchanged.
- Exact apply reported one pruned body; retry reported one already-pruned body and no second mutation.
- Full-body JSON returned an empty message plus `body_unavailable_reason=retention`; body-free identity, time, client, agent, session, and workspace fields remained stable.
- Restore reported one restored body; retry reported one already-restored body. The original 56-byte payload and full-body query returned, and `PRAGMA integrity_check` was `ok`.
- `TestRawBodyRetention_interruptionResumesFromDurableBatch` injects interruption after a durable batch and proves retry converges with one resumed and one already-durable candidate.

The unchanged allocated size is expected: logical body pruning does not imply immediate SQLite page reclamation, and retention does not invoke compaction.

## Archive and backup drill

Each disposable root began with two current-generation verified files. Count ceiling `1` selected the older file and protected the newer recovery floor.

| Class | Before logical / allocated | After logical / allocated | Retained floor |
|---|---:|---:|---|
| archive | 1,678 / 8,192 bytes | 839 / 4,096 bytes | `new.trcaryar` |
| backup | 524,288 / 524,288 bytes | 262,144 / 262,144 bytes | `new.db` |

Evidence:

- Plan `aae96eb0b69f4690418ffc2711b0c6bdfe3236ea3fcf779305352282d2f140f3` selected `old.trcaryar` and `old.db` for `count`.
- A wrong confirmation left the complete root evidence hash unchanged.
- Exact apply deleted two candidates with zero conflicts; retry reported two already-committed candidates.
- The deleted backup sidecar was removed and the retained-floor sidecar remained.
- The raw-body recovery archive with SHA-256 `927d130e00e98586fb5caace14803f7170df1df82c10a593715bef895fd63db1` passed `store archive verify`, then restored into a new disposable database: dry-run and apply reported `inserted=1 skipped=0 conflicts=0 total=1`.
- The restored archive database passed `PRAGMA integrity_check=ok`; a metadata query returned the exact event/session/kind/client/agent/workspace identity with an available 56-byte body, and `traceary show` returned the original full payload.
- The retained SQLite backup also restored into a copied database with representative event count `1` and `PRAGMA integrity_check=ok`.
- The parameterized file-retention crash matrix covers every journal and namespace boundary; retry converges to one committed journal, one exact ledger entry, deleted catalog state, absent candidate/tombstone, and present floor.

## Doctor/status evidence

The capacity-root portion of `traceary doctor --archive-root ... --backup-root ... --json` is read-only: it neither creates nor applies a retention plan, and it reported both retained roots in the Database section. `doctor` as a whole is not a read-only command: it initializes or migrates the configured SQLite database, and `--fix` can run unrelated reviewed remediations. These effects are separate from capacity-root inspection.

- archive: `state=ready files=1 verified=1 logical_bytes=839 allocated_bytes=4096 floor=new.trcaryar`
- backup: `state=ready files=1 verified=1 logical_bytes=262144 allocated_bytes=262144 floor=new.db`
- both messages explicitly stated that automatic cleanup is disabled and linked to the manual plan command.

Dogfood found that the first status implementation could report `ready` for a group/other-writable root even though exact apply rejects that permission boundary. Follow-up #1486 records and fixes this release-blocking mismatch before v0.31 release.

## Rollback and residual risk

Rollback is the reviewed restore into a disposable database followed by integrity and representative query checks. No QA drill replaces the live database. File-capacity rollback uses the protected verified floor; raw-body rollback uses the digest-pinned recovery archive.

The filesystem contract trusts processes running as the caller UID. Apply refuses roots not owned by the caller or writable by group/other users, and Traceary writers share the root lock. A malicious same-UID process is out of scope because portable Unix has no conditional unlink-by-inode primitive and that UID can already modify the Traceary database and configuration.
