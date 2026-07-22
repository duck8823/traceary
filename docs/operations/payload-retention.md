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
| `RetentionPlan` | version, plan ID, creation snapshot, source fingerprint, policy, candidates | Immutable review artifact | The plan ID hashes canonical content with `plan_id` omitted; apply never re-plans |
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

Allocated bytes are an estimate of storage occupied by the selected payload or file, not a promise that the filesystem will immediately reclaim them. SQLite row pruning reports logical reclaimable bytes; physical reclaim requires a separately requested compaction phase. Candidate-level SQLite allocated bytes are not additive and cannot drive `raw_body` selection in v0.31; database allocated bytes are observation-only before/after compaction.

Every configured ceiling is evaluated independently against the projected post-plan state as `satisfied`, `unsatisfied`, or `indeterminate`. The class result is an AND reduction: any `unsatisfied` ceiling makes the class `unsatisfied`; otherwise any `indeterminate` ceiling makes it `indeterminate`; only all `satisfied` ceilings produce `satisfied`. An unknown current measurement or unknown candidate extent for any configured byte ceiling is `indeterminate`, including when age/count ceilings are also configured. Unsupported allocated-byte measurement is also `indeterminate`. `indeterminate` and `unsatisfied` plans cannot be applied.

| Class | Age | Count | Logical bytes | Allocated bytes | Zero budget in v0.31 |
|---|---|---|---|---|---|
| `raw_body` | candidate selector | candidate selector | candidate selector | observation only | rejected |
| `metadata` | report only | report only | report only | observation only | rejected; no executor |
| `aggregate` | report only | report only | report only | observation only | rejected; no executor |
| `archive` | file selector | file selector | known file-size selector | known file-block selector when supported | rejected |
| `backup` | file selector | file selector | known file-size selector | known file-block selector when supported | rejected |

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

- `schema_version` and canonical `plan_id` computed with `plan_id` omitted;
- `created_at` and one UTC `snapshot_at`;
- sanitized source identity: database file identity, SQLite `user_version`, migration-set digest, and file/root fingerprints without payload content;
- the effective policy and all configured ceilings;
- current counts/extents, candidate counts/extents, excluded counts, and availability for every byte value;
- exact database identities or root ID plus normalized root-relative file paths, timestamps, reasons, and required recovery point;
- independent phase sections for body pruning, archive cleanup, backup rotation, and compaction;
- warnings, including unknown extents and missing optional recovery coverage.

Plan JSON never includes event body, command input/output, archive passphrase, credentials, absolute home paths, or file contents.

Canonical hashing follows RFC 8785 JSON canonicalization over a normative `canonical_payload`. The top-level plan has exactly `plan_id`, `canonical_payload`, and optional `display`; apply parses only `canonical_payload`, rejects unknown canonical fields, and ignores `display`. `canonical_payload` contains `schema_version`, `created_at`, `snapshot_at`, `source`, `policy`, `class_results`, `candidates`, `exclusions`, `recovery_requirements`, and `phases`. No other field is excluded from the hash.

Optional canonical fields are omitted, never encoded sometimes as `null`. All byte values and nanosecond durations are unsigned base-10 strings without leading zeroes (except `"0"`), avoiding RFC 8785/IEEE-754 safe-integer ambiguity. Counts are JSON integers constrained to 0 through 9,007,199,254,740,991. Instants are UTC RFC3339Nano. Root-relative paths use slash separators without `.`, `..`, or empty segments.

The machine-readable schema is [`schema/retention-plan.schema.json`](../../schema/retention-plan.schema.json). It fixes required/optional fields, types, `additionalProperties=false`, and enum order. The enum orders are retention class `raw_body < metadata < aggregate < archive < backup`; ceiling/reason `age < count < logical_bytes < allocated_bytes < retain < debug < legal_hold < active_session < recovery_floor < unknown_extent < path_escape < unverified < stale`; phase `body_prune < archive_cleanup < backup_rotation < compaction`; status `satisfied < unsatisfied < indeterminate`.

All database and file candidates carry every comparison field. An inapplicable value is the empty string, not a missing key: database candidates use empty `root_id`/`relative_path`; file candidates use empty `database_identity`. The total ordering for set-like arrays is fixed per schema: class results by class enum; ceilings by ceiling enum; candidates by class, database identity, root ID, relative path, timestamp, candidate identity; reasons/exclusions by reason enum then stable identity; recovery points by generation, digest, root ID, relative path; phases/batches by phase enum then decimal-string batch ordinal. If all listed keys tie, the RFC 8785 bytes of the element are the final comparison key. Order-significant arrays are explicitly named `ordered_steps` and are not sorted.

The checked-in golden vector consists of [`retention-plan-canonical.golden.json`](./testdata/retention-plan-canonical.golden.json) and [`retention-plan.golden.json`](./testdata/retention-plan.golden.json). The canonical fixture is the exact RFC 8785 byte sequence with no trailing newline. Its SHA-256 digest is the `plan_id` in the complete plan fixture; implementations in every language must match both values.

The SHA-256 hash detects accidental or operator modification; it is not an authenticity signature. Changing `display` cannot affect execution because no apply decision reads it. CLI confirmation displays the plan ID and exact phase.

## Apply, interruption, and retry

Apply rejects a plan when its hash, version, database identity, SQLite `user_version`, migration digest, configured roots, or recovery prerequisite differs. For body pruning, each exact candidate also carries a compare-and-set fingerprint of the fields that establish eligibility. A newly held, modified, or already-pruned identity is a stale-plan error; the transaction fails closed without partially applying the reviewed batch. Hold rows, recovery pins, candidate fingerprints, and the unique execution-ledger transition are checked in the same SQLite write transaction. A hold added before commit therefore causes zero body deletion.

Body pruning is a bounded transaction per plan batch. The execution ledger records the plan/phase/batch outcome in the database transaction. Re-running a completed batch is an idempotent no-op. A process interruption before commit changes nothing; an interruption after commit is detected from the ledger.

File cleanup uses a directory handle, refuses symlinks, and compares device/inode/size/mtime plus digest immediately before a same-directory tombstone rename without re-resolving the path. It then fsyncs the directory, records `tombstoned`, unlinks through the same directory handle, fsyncs again, and records `committed`. A durable state machine (`pending`, `running`, `tombstoned`, `committed`, `conflicted`, `failed`) makes every crash point retryable: a matching tombstone resumes unlink, an original replacement conflicts, and committed is a no-op. Missing candidates are accepted only when the execution ledger and tombstone identity prove the reviewed file was removed.

Recovery points also have `active`, `deleting`, and `deleted` states. Pin acquisition and `active -> deleting` use one RecoveryCatalog CAS: a pin succeeds only for `active`; deletion reservation succeeds only when pin count is zero and no protected floor references the point. The rotation lease is held from that CAS through tombstone rename, both directory fsyncs, unlink, catalog `deleted`, and execution-ledger commit. A body apply racing a `deleting` point fails before its SQLite body transaction and must re-plan. A rotation racing a pin fails its CAS. Crash recovery retains the lease/fencing token, reconciles catalog state with the exact tombstone identity, and either completes deletion or returns the point to `active` only when the original verified file was restored.

Compaction is never implicit. It runs after database integrity and representative metadata/full-body queries pass, creates or verifies its own recovery point, and reports allocated-size change independently from logical pruning. The implementation uses a new `VACUUM INTO` database rather than mutating the live file in place, requires an exclusive writer lease, verifies free space for source plus destination plus safety margin, runs `integrity_check` on the destination, and uses the recoverable single-database replacement state machine below. An interrupted or invalid `VACUUM INTO` output is discarded and never becomes authoritative.

## Recovery and rollback

Raw-body apply requires a verified archive or backup that covers the selected identities unless the operator selects an explicit future `discard_without_recovery` policy. That policy is outside v0.31. The recovery point is reverified immediately before the body transaction begins and pinned in that transaction. Restore is primary-key/idempotency aware and must distinguish restored, skipped-identical, and conflicting content.

One `RecoveryCatalog` owns pins and the retention floor across archive and backup classes. A recovery generation identifies the source database fingerprint plus coverage manifest. Every destructive execution pins a recovery point covering its exact identities until `rollback_until` expires and an explicit release succeeds. Rotation is serialized by catalog lease/CAS, and budgets cannot remove pinned points. Archive and backup rotation also preserves the newest verified recovery point for the current live generation. If the catalog has zero verified recovery points, v0.31 reports candidates but deletes no archive or backup; an unverified potential file is not a recovery floor.

`verified` requires root confinement and regular-file identity, SHA-256 verification, a supported format, a coverage manifest, availability of the decryption key through the named environment variable when encrypted, successful decode/SQLite `integrity_check`, and a copied-store restore rehearsal recorded for the same digest. Digest verification alone is insufficient. Recovery pins and rotation decisions use the digest and coverage generation, not a mutable path.

Rollback means stopping automatic execution (disabled by default), restoring the verified recovery point or archive to a copied database, validating it, and replacing the live database only through the strengthened restore contract. The current `VACUUM INTO` backup and staged-rename restore are a starting point, not sufficient proof for v0.31 execution.

Replacement acquires an exclusive cross-process writer lease and fencing token, rejects new opens, checkpoints WAL with `TRUNCATE`, closes every Traceary connection, and proves that no writer remains and WAL/SHM sidecars are absent. From that point only the single main database file participates. On restart, a new lease/fencing token is acquired; operations bearing the old token are rejected rather than treated as retaining a lease.

Every journal update uses the same persistence primitive: write a complete record with monotonic sequence, fencing token, state, paths, and digests to a same-directory temporary file; fsync it; rename it over the journal; fsync the directory. Only after an intent state is durable may its namespace operation run. After each file rename, fsync the directory before writing the corresponding completed state. A same-directory durable swap journal records:

1. `prepared`: candidate database is synced, digest-matched, and passes `integrity_check`; live database still owns the canonical path.
2. `old_move_intent`: durable intent to rename live database to the journal-named old generation.
3. rename live to old, fsync directory, then persist `old_staged`.
4. `new_move_intent`: durable intent to rename candidate to the canonical path.
5. rename candidate to canonical, fsync directory, then persist `new_placed`.
6. reopen the exact canonical path under the fencing token, run migrations/read checks and `integrity_check`, then persist `verified`.
7. commit catalog/ledger, fsync them, persist `committed`, and retire the old generation only after its rollback pin is released.

Recovery never chooses between rolling forward and rolling back implicitly. `old_staged` always rolls forward because `prepared` already proved the candidate; aborting a plan requires an explicit durable abort request before `old_move_intent`. If canonical-new verification fails after placement, recovery uses these additional journal states: `rollback_new_quarantine_intent`, `rollback_new_quarantined`, `rollback_old_restore_intent`, and terminal `rolled_back`. Each rename follows the same intent, rename, directory-fsync, completed-state sequence. A rolled-back execution never returns to `prepared`; a new candidate requires a new plan and journal.

The two renames are not claimed to be one atomic operation. Recovery uses this complete state/name matrix; any unlisted combination is `conflicted` and performs no deletion:

| Journal state | Canonical | Old | Candidate | Recovery |
|---|---|---|---|---|
| `prepared` | old digest | absent | new digest | persist `old_move_intent` and continue |
| `old_move_intent` | old digest | absent | new digest | retry old rename |
| `old_move_intent` | absent | old digest | new digest | namespace operation completed; persist `old_staged` |
| `old_staged` | absent | old digest | new digest | persist `new_move_intent` and continue |
| `new_move_intent` | absent | old digest | new digest | retry new rename |
| `new_move_intent` | new digest | old digest | absent | namespace operation completed; persist `new_placed` |
| `new_placed` or `verified` | valid new digest | old digest | absent | verify/continue new |
| `new_placed` or `verified` | invalid new | old digest | absent | persist `rollback_new_quarantine_intent` |
| `rollback_new_quarantine_intent` | new digest | old digest | absent | rename canonical new to journal-named quarantine, fsync, persist `rollback_new_quarantined` |
| `rollback_new_quarantine_intent` | absent | old digest | absent; quarantine has new digest | prior rename completed; persist `rollback_new_quarantined` |
| `rollback_new_quarantined` | absent | old digest | absent; quarantine has new digest | persist `rollback_old_restore_intent` |
| `rollback_old_restore_intent` | absent | old digest | absent; quarantine has new digest | rename old to canonical, fsync, persist `rolled_back` |
| `rollback_old_restore_intent` | old digest | absent | absent; quarantine has new digest | prior rename completed; persist `rolled_back` |
| `rolled_back` | old digest | absent | absent; quarantine has new digest | terminal no-op; a new plan is required to retry |
| `committed` | valid new digest | old or absent | absent | keep new; old remains pinned or is retired after release |

If both canonical and old contain the old digest after an intent, or a path has an unknown digest, recovery conflicts rather than guessing. WAL/SHM are never copied or renamed as a multi-file unit. No process may retain an open writer during replacement. Additive schema and execution-ledger tables may remain when rolling back the binary; older binaries ignore them. Down migrations do not reconstruct deleted bodies.

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
| plan command with DB/WAL and file roots | compare before/after | database, sidecars, roots, markers, and temporary files are unchanged | integration |
| byte-only budget with unknown extent | plan | result is `indeterminate` and apply is unavailable | application/integration |
| hold added after plan or during apply | apply | transaction deletes zero bodies | SQLite integration |
| two concurrent rotations | apply | catalog CAS serializes them and one verified floor remains | integration |
| body prune followed by rotation | rotate | pinned covering recovery point remains until explicit release | integration |
| every selected identity | render plan | identity and every selection reason are present | application/CLI |
| canonical golden plan | hash in Go and fixture verifier | exact canonical bytes and plan ID match | domain/tooling |
| pin races `active -> deleting` | interleave both CAS paths | exactly one wins; no pinned file is unlinked | integration |
| crash at every file deletion state | restart | file/catalog/ledger converge without wrong-path deletion | integration |
| crash at every DB swap state | restart | exactly old or verified new generation becomes canonical | SQLite integration |

## TDD and split-PR plan

1. #1446: merge this contract only; no schema or behavior change.
2. #1444, internal step A: value objects, additive schema, read-only planner, plan CLI, golden canonical vector, and dry-run side-effect tests.
3. #1444, internal step B: recovery verification/restore tests, recovery catalog/pins, execution ledger, and all DB swap crash states.
4. #1444, internal step C: body executor behind an internal boundary; do not expose apply yet. Repository policy keeps one Issue/PR, so A/B/C are separate commits and review checkpoints within the single #1444 PR; each must pass its focused tests before the next commit.
5. #1443: archive/backup inventory and rotation executor with root confinement, catalog CAS, pinned-floor tests, and no public apply until recovery drills pass.
6. #1445: copied-store interruption/retry/restore/rollback drills, doctor/status, then opt-in public apply and final default decision.

Implementation starts with failing observable behavior tests, adds the smallest domain/application boundary, then the SQLite/filesystem adapters and CLI. Handler call order and private helpers are not test contracts.

## Default decision checkpoint

v0.31 ships with retention execution disabled by default. A default may change only in a later release after copied-store dogfood demonstrates recovery, bounded runtime, acceptable unknown-byte rates, stable metadata/aggregate output, and no release-blocking findings. No private body is uploaded to external storage.
