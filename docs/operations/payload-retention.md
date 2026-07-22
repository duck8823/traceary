# Payload retention and capacity contract

[日本語](./payload-retention.ja.md)

Status: v0.31.0 design checkpoint for Issue #1446. This document defines the contract for #1444, #1443, and #1445; it does not enable deletion.

## Requirement summary

Traceary must bound local storage without treating resumable session metadata and short-lived raw payloads as one retention class. Planning must be read-only, execution must use the exact reviewed plan, and install or upgrade must never apply retention automatically.

The first implementation remains opt-in. It operates on a copied or synthetic store during dogfood and separates four effects: raw-body pruning, archive cleanup, backup rotation, and SQLite compaction.

## Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| `RetentionClass` | `raw_body`, `metadata`, `aggregate`, `archive`, `backup` | Selects an independent budget and action | A row or file is never deleted through an unrelated class |
| `RetentionBudget` | optional age, count, logical bytes, allocated bytes | Produces candidates until every configured ceiling is satisfied | Omitted, unknown, and zero are different values |
| `RetentionHold` | retained, debug, legal/operational hold, reason, optional expiry | Excludes an identity from planning | A hold is evaluated before ordering or byte limits |
| `CapacityExtent` | known logical bytes, known allocated bytes, or unknown | Reports current and reclaimable capacity | Unknown is serialized as availability, never numeric zero |
| `RetentionCandidate` | class, exact identity/path, timestamps, extents, reasons | Explains one proposed action | Every candidate has at least one machine-readable reason |
| `RetentionPlan` | version, plan ID, creation snapshot, source fingerprint, policy, candidates | Immutable review artifact | The plan ID hashes canonical plan content; apply never re-plans |
| `RecoveryPoint` | archive/backup path, digest, verified-at, covered identities | Authorizes destructive body/file removal | Unverified or corrupt material never authorizes deletion |
| `RetentionExecution` | planned phase, outcomes, interruption marker | Applies one phase idempotently | Later phases cannot silently run when an earlier prerequisite failed |

### Retention classes

- `raw_body`: event bodies and command input/output payloads. Pruning preserves event IDs, session IDs, timestamps, kinds, source metadata, body extents, and aggregates. A pruned body remains distinguishable from an originally empty or unavailable body.
- `metadata`: session/event identity and provenance required for audit, filtering, lineage, and aggregates. v0.31 defines its budget but does not physically delete it.
- `aggregate`: derived summaries or persisted aggregate state. v0.31 defines its budget but does not let aggregate cleanup authorize primary-row deletion.
- `archive`: verified Traceary archive packages. Rotation is independent from live-row pruning.
- `backup`: SQLite pre-operation backups and other local recovery copies. Rotation preserves a newest verified recoverable point.

## Budget semantics

Each class may configure any combination of:

```json
{
  "max_age": "336h",
  "max_count": 20,
  "max_logical_bytes": 1073741824,
  "max_allocated_bytes": 2147483648
}
```

An omitted ceiling is unlimited. Numeric zero is an explicit zero budget and is valid only when the class and command allow complete removal; it is not the representation of unknown measurement. Measurement uses `{ "availability": "known", "bytes": 0 }` or `{ "availability": "unknown" }`.

Candidates are ordered deterministically by class-specific age, stable identity, and path. A candidate may have multiple reasons (`age`, `count`, `logical_bytes`, `allocated_bytes`) but appears once. Byte ceilings use measured extents only. Unknown extents remain visible and cannot be assumed reclaimable; count/age rules may still select them.

Allocated bytes are an estimate of storage occupied by the selected payload or file, not a promise that the filesystem will immediately reclaim them. SQLite row pruning reports logical reclaimable bytes; physical reclaim requires a separately requested compaction phase.

## Holds and exclusions

Planning excludes:

- explicit identity holds (`retain`, `debug`, or `legal_hold`-equivalent operational hold);
- bodies newer than the configured age boundary;
- active or otherwise non-terminal sessions unless a later policy explicitly permits them;
- the newest verified recovery point needed by the selected destructive phase;
- files outside the configured archive/backup roots after symlink resolution;
- unverified, corrupt, or partially written archive/backup files from deletion authorization.

The plan reports excluded counts and reasons without serializing raw body content. A hold expiry is evaluated against the plan snapshot, not wall clock during apply.

## Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| Budget and plan invariants | domain value objects | Cobra handlers and SQL queries |
| Plan orchestration and prerequisite checks | application use case | domain model and presentation |
| Body candidate projection, exact updates, byte measurement | SQLite adapter | use case |
| Archive/backup root enumeration and file identity | filesystem adapter | domain model |
| CLI input/output and confirmation boundary | presentation/CLI | application business rules |
| Archive package verification/restore | existing archive use case and codec | retention planner |
| Compaction | explicit store maintenance adapter | body-prune execution |

The planner consumes body-free candidate projections and file metadata. The use case never receives SQL rows, table names, open file handles, or raw private bodies.

## Boundaries and interfaces

Consumer-oriented ports are split by intent instead of a `dryRun bool`:

```text
RetentionPlanner.Plan(ctx, RetentionPolicy) -> RetentionPlan
RetentionExecutor.ApplyBodyPlan(ctx, ReviewedRetentionPlan) -> PhaseResult
RetentionExecutor.ApplyArchivePlan(ctx, ReviewedRetentionPlan) -> PhaseResult
RetentionExecutor.ApplyBackupPlan(ctx, ReviewedRetentionPlan) -> PhaseResult
StoreCompactor.Compact(ctx, ReviewedCompactionPlan) -> CompactionResult
RecoveryVerifier.Verify(ctx, RecoveryPoint) -> VerifiedRecoveryPoint
```

`ReviewedRetentionPlan` can only be created after parsing a supported plan version, recomputing its canonical hash, matching the source database/file roots, and confirming all destructive prerequisites. Planning has no writer dependency and cannot create backups, markers, archives, or temporary plan files unless the caller explicitly redirects the returned JSON to a file.

## Immutable plan format

The JSON plan includes:

- `schema_version` and canonical `plan_id`;
- `created_at` and one UTC `snapshot_at`;
- sanitized source identity: database file identity, SQLite `user_version`, migration-set digest, and file/root fingerprints without payload content;
- the effective policy and all configured ceilings;
- current counts/extents, candidate counts/extents, excluded counts, and availability for every byte value;
- exact database identities or canonical local file paths, timestamps, reasons, and required recovery point;
- independent phase sections for body pruning, archive cleanup, backup rotation, and compaction;
- warnings, including unknown extents and missing optional recovery coverage.

Plan JSON never includes event body, command input/output, archive passphrase, credentials, or file contents.

## Apply, interruption, and retry

Apply rejects a plan when its hash, version, database identity, SQLite `user_version`, migration digest, configured roots, or recovery prerequisite differs. For body pruning, each exact candidate also carries a compare-and-set fingerprint of the fields that establish eligibility. A newly held, modified, or already-pruned identity is a stale-plan error; the transaction fails closed without partially applying the reviewed batch.

Body pruning is a bounded transaction per plan batch. The execution ledger records the plan/phase/batch outcome in the database transaction. Re-running a completed batch is an idempotent no-op. A process interruption before commit changes nothing; an interruption after commit is detected from the ledger.

File cleanup uses same-directory atomic markers and exact file identities. It verifies the retained recovery point again immediately before deletion. Missing candidates are reported as already absent only when their recorded identity proves they were the reviewed file; replacement at the same path is a conflict.

Compaction is never implicit. It runs after database integrity and representative metadata/full-body queries pass, creates or verifies its own recovery point, and reports allocated-size change independently from logical pruning.

## Recovery and rollback

Raw-body apply requires a verified archive or backup that covers the selected identities unless the operator selects an explicit future `discard_without_recovery` policy. That policy is outside v0.31. Restore is primary-key/idempotency aware and must distinguish restored, skipped-identical, and conflicting content.

Archive and backup rotation preserve at least the newest verified recovery point for the live database generation. Age/count/byte budgets cannot override this floor. If no verified recovery point exists, rotation may report candidates but cannot delete the last potential recovery file.

Rollback means stopping automatic execution (disabled by default), restoring the verified recovery point or archive to a copied database, validating it, and atomically replacing the live database only through the existing backup/restore contract. Additive schema and execution-ledger tables may remain when rolling back the binary; older binaries ignore them. Down migrations do not reconstruct deleted bodies.

Rollback triggers include candidate/byte drift, integrity failure, metadata projection change, aggregate mismatch, recovery verification failure, path escape, unexpected hold bypass, or any write during a nominal plan operation.

## Behavior tests

| Given | When | Then | Level |
|---|---|---|---|
| unknown and zero extents | serialize a plan | states remain distinct | domain/application |
| held and eligible rows | plan raw bodies | held rows are excluded with reasons | application/integration |
| large bodies with unchanged metadata | prune reviewed bodies | metadata-only output is byte-equivalent and full-body output says retained body unavailable | CLI/MCP integration |
| modified candidate after planning | apply | whole batch fails as stale with no extra pruning | SQLite integration |
| process interruption before/after commit | retry | zero or one effective application | SQLite integration |
| corrupt recovery package | apply destructive phase | deletion is refused | use case/integration |
| mixed archive files and symlink escape | plan/apply rotation | outside-root target is rejected | filesystem integration |
| budgets would remove every backup | rotate | newest verified recovery point remains | application/integration |
| pruning without compaction | inspect sizes | logical bytes decrease while allocated bytes may remain | dogfood |
| explicit compaction after verification | compact | integrity passes and allocated delta is reported | dogfood |

## TDD and split-PR plan

1. #1446: merge this contract only; no schema or behavior change.
2. #1444: add retention value objects, additive body-extent/availability and execution-ledger migration, body planner/apply ports, SQLite implementation, CLI plan/apply, and behavior tests. Keep all defaults disabled.
3. #1443: add archive/backup inventory and rotation plans with root confinement and recovery-point floor.
4. #1445: add doctor/status reporting, copied-store interruption/retry/restore/rollback evidence, and the final opt-in/default decision.

Implementation starts with failing observable behavior tests, adds the smallest domain/application boundary, then the SQLite/filesystem adapters and CLI. Handler call order and private helpers are not test contracts.

## Default decision checkpoint

v0.31 ships with retention execution disabled by default. A default may change only in a later release after copied-store dogfood demonstrates recovery, bounded runtime, acceptable unknown-byte rates, stable metadata/aggregate output, and no release-blocking findings. No private body is uploaded to external storage.
