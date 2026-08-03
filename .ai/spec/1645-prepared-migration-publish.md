# #1645 prepared migration publication — high-risk design checkpoint

## Decision

Data-dependent rehearsal migrations must not run against the copied rehearsal
target. They are applied to a run-owned, same-filesystem candidate, which is
checkpointed, closed, synced, and verified before it is published. Publication
uses the compaction durable intent/orientation/atomic-exchange implementation;
there must be one exchange implementation and one recovery state machine in the
repository.

This is an infrastructure maintenance protocol, not a general-purpose domain
plugin system. There are two real recipes: compaction and migration. The shared
aggregate owns file-orientation and crash-recovery decisions; each recipe owns
how a candidate is built and how logical equivalence is proved. The payload
rehearsal use case sees a consumer-oriented `RehearsalTargetPreparation` port,
not compaction types, raw migration SQL, inode tuples, or boolean execution
flags.

## Requirement summary

- Classify every pending embedded migration explicitly as `constant_in_place`
  or `data_dependent_offline`. Missing classifications and classification/body
  digest mismatches fail before the target, backup, or candidate is changed.
- If any pending migration is offline, apply the complete ordered pending suffix
  (35 through 43 for a v34 copied store) to one owned candidate. Do not publish a
  half-current schema and do not run migration 35 under the one-second lock.
- Bound candidate wall time, aggregate owned disk, WAL, and SQLite temporary
  work. Any build/verification failure leaves target bytes and schema unchanged.
- Apply the same direct-SQLite store-format compatibility policy independently
  to target and candidate, then compare versioned canonical event/audit evidence.
- Hold the adjacent exclusive advisory lock only for publication or physical
  recovery. Copy, SQL migration, checkpoint, compatibility checks, integrity
  checks, canonical digesting, and payload scrub are outside that lock.
- After publication, create or resume the payload rehearsal run in a separate
  existing WAL-budgeted transaction. A crash between publication and run
  creation remains resumable from the external upgrade journal.
- Rollback restores the original target inode orientation. The configured live
  store is never opened writable by this workflow.

Non-goals: changing migration SQL, activating canonical zstd writes, replacing
the configured live store, inventing a Windows multi-rename emulation, or
turning migration files into runtime-selected plugins.

## Existing architecture to preserve

- `domain/compaction_run.go` already owns the valid phase/orientation/action
  matrix and distinguishes stable inode identity from mutable file attributes.
- `application/store_compaction.go` already separates journal, candidate builder,
  candidate ownership, replacement coordination, and exclusive lease ports.
- `infrastructure/sqlite/compaction_files.go` is the only durable JSONL journal,
  no-follow identity fence, atomic exchange, no-replace rollback publication,
  directory sync, and orientation observation implementation.
- `infrastructure/sqlite/compaction_exchange_{darwin,linux,unsupported}.go` is the
  only platform exchange primitive.
- `infrastructure/sqlite/compaction_sqlite.go` contains the sidecar-free direct
  opener, common compatibility call, integrity/FK scrub, schema fingerprint,
  and order-independent logical digest machinery.
- `infrastructure/sqlite/payload_rehearsal.go:340-498` currently measures an
  exact clone and then migrates the target in place before creating the run.
  That block is replaced by the target-preparation port; batch, scrub, pause,
  and run state remain downstream behavior.
- `infrastructure/sqlite/store_lease.go` keeps normal Traceary physical
  connections on the adjacent shared lease. Its exclusive lease remains the
  publication/recovery fence.

Compaction's CLI JSON and existing `.traceary-compaction/*.jsonl` journals are a
compatibility surface. Extraction must retain their field names/state strings
and load old records in golden tests. The migration workflow uses a separate
journal namespace, but the same journal implementation and aggregate.

## Conceptual model

| Concept | State / evidence | Behavior | Constraint / invariant |
|---|---|---|---|
| Upgrade operation | `compaction`, `payload_rehearsal_migration` | Selects one registered recipe on fresh-process resume | Immutable and journaled; unknown operations fail closed |
| Migration catalog | ordered version, basename, body SHA-256, class | Inventories the exact pending suffix | No SQL-text heuristic; missing/duplicate/gapped/changed entries fail closed |
| Prepared upgrade run | phase, target/candidate/rollback paths, operation and consumer binding | Decides the next normal or recovery action | Journal lives outside every exchanged SQLite inode and is capped |
| Target binding | canonical path; device, inode, size, mtime, mode, link count; initial file digest | Fences the copied rehearsal target | Regular, single-link, no symlink ancestor, sidecar-free at exchange |
| Owned candidate | absent, prepared empty inode, building, synced, verified | Receives the copied bytes and pending migrations | Same directory/filesystem; only the recorded prepared inode may be deleted/retried |
| Preparation evidence | migration-set digest, source/candidate compatibility, schema, canonical evidence, peak resources | Authorizes publication | Evidence is immutable after `candidate_verified` |
| Orientation | original, candidate-ready, exchanged, rollback-ready, rolled-back | Reconciles physical placement after a crash | Derived from observed inodes, never inferred only from the last record |
| Publication lease | waiting, held, released | Excludes shared-lease Traceary connections | No data-dependent work while held; elapsed acquisition-to-release is recorded |
| Rehearsal handoff | publication receipt, no run / active run | Creates or resumes the DB-resident rehearsal run | Separate transaction; receipt binds target, config hash, backup digest, and migration digest |
| Rollback authority | immutable original inode plus original digest | Restores original orientation by exchange | Never reconstruct/downgrade schema in place; never delete both valid copies |

### Migration classification for the current catalog

The classification is an explicit source-owned manifest keyed by version and
SQL body digest. It is reviewed with each migration change. For 35-43:

| Version | Class | Reason |
|---|---|---|
| 35 | `data_dependent_offline` | `CREATE INDEX` scans historical projection rows |
| 36 | `constant_in_place` | additive DDL and constant singleton/trigger creation; no historical row query |
| 37 | `constant_in_place` | empty rehearsal tables/index/triggers |
| 38 | `constant_in_place` | empty projection schema/trigger creation; SELECT text inside triggers is not executed at install |
| 39 | `constant_in_place` | empty fingerprint schema/trigger creation |
| 40 | `constant_in_place` | singleton control schema and constant insert |
| 41 | `data_dependent_offline` | `INSERT ... SELECT` reads existing projection state, although bounded to one row |
| 42 | `data_dependent_offline` | compatibility update reads whether canonical events exist |
| 43 | `constant_in_place` | schema plus constant `INSERT OR IGNORE` |

Classification means data dependence, not an estimated duration. Once an
offline entry exists, the whole pending suffix runs on the candidate so one
publication establishes one exact current-schema state. A target with missing
pre-35 migrations, a non-prefix applied set, unknown future migrations, or a
manifest/body digest mismatch is unsupported by this workflow and remains
unchanged. An all-constant pending suffix may use the existing bounded in-place
executor; it must not silently fall back there after an offline-plan failure.

## Responsibility assignment

| Responsibility | Owner | Reason to change | Explicit non-owner |
|---|---|---|---|
| Phase/orientation/invariant decisions | `domain.PreparedStoreUpgradeRun` extracted from `CompactionRun` | Crash protocol changes | Use case switch statements and SQLite adapter |
| Offline vs constant catalog and exact pending plan | SQLite migration catalog/planner | Embedded files and SQLite ledger | Payload use case, CLI, SQL keyword scanner |
| Candidate copy, migration, WAL/temp/checkpoint/close | SQLite migration candidate recipe | SQLite execution and resource behavior | Shared publisher and rehearsal use case |
| Compatibility/integrity/canonical evidence | Direct SQLite verifier | Store schema/codecs and canonical row mapping | Generic file coordinator |
| Journal append/load and physical orientation | Shared prepared-upgrade journal/files adapter | Filesystem durability and syscalls | Recipe and payload adapter |
| Offline prepare vs short locked publish sequencing | `PreparedStoreUpgradeUsecase` | Maintenance workflow | Domain aggregate and CLI |
| Rehearsal-specific config/backup/handoff | `RehearsalTargetPreparation` adapter | Payload workflow contract | Generic upgrade aggregate |
| Batch/pause/resume/scrub after handoff | Existing payload rehearsal workflow | Codec rehearsal behavior | Upgrade publisher |
| Flags/JSON/errors | CLI | Protocol contract | Infrastructure |

The existing `PayloadRehearsalAdapter.Prepare` is already large and procedural.
It must not absorb the exchange journal/state machine. Split target preparation
from run-handle construction; do not add another set of rename/recovery helper
functions to `payload_rehearsal*.go`.

## Boundaries and interfaces

Names are proposed; exact file names may vary, but responsibility and method
shape are the checkpoint.

```go
// Domain/application-safe values: no raw SQL or open SQLite handles.
type PreparedStoreUpgradeBudget struct {
    WallTimeLimit       time.Duration
    PublishLockLimit    time.Duration
    OwnedDiskByteLimit  uint64 // candidate main + WAL/SHM/journal + owned temp
    WALByteLimit        uint64
    SafetyMarginBytes   uint64
}

type PreparedStoreUpgradeCommand struct {
    Operation       PreparedStoreUpgradeOperation
    TargetPath      string
    ConsumerBinding string // hash(config + backup + migration plan), not a bool flag
    Budget          PreparedStoreUpgradeBudget
}

type PreparedCandidateRecipe interface {
    Build(context.Context, PreparedCandidateRequest) (PreparedCandidateEvidence, error)
    ClassifyOwnedCandidate(context.Context, PreparedCandidateInspection) (CandidateCondition, error)
}

type PreparedStoreUpgradeJournal interface {
    Create(context.Context, PreparedStoreUpgradeRun) error
    Load(context.Context, string) (PreparedStoreUpgradeRun, error)
    FindActive(context.Context, PreparedStoreUpgradeOperation, string, string) (PreparedStoreUpgradeRun, error)
    Append(context.Context, PreparedStoreUpgradeRun) error
}

type PreparedStoreUpgradeFiles interface {
    Plan(context.Context, PreparedStoreUpgradeRun) (PreparedStoreUpgradeRun, error)
    PrepareCandidate(context.Context, PreparedStoreUpgradeRun) (StoreFileIdentity, error)
    RemoveOwnedPartialCandidate(context.Context, PreparedStoreUpgradeRun, PreparedStoreObservation) error
    RecheckForPublish(context.Context, PreparedStoreUpgradeRun) error
    FenceCandidate(context.Context, PreparedStoreUpgradeRun) (PreparedStoreUpgradeRun, error)
    Exchange(context.Context, PreparedStoreUpgradeRun) error
    PublishRollback(context.Context, PreparedStoreUpgradeRun) error
    Observe(context.Context, PreparedStoreUpgradeRun) (PreparedStoreObservation, error)
    SyncRecoveredOrientation(context.Context, PreparedStoreUpgradeRun, PreparedStoreObservation) error
}

type PreparedStoreUpgradeUsecase interface {
    Plan(context.Context, PreparedStoreUpgradeCommand) (PreparedStoreUpgradeRun, error)
    Prepare(context.Context, string) (PreparedStoreUpgradeRun, error) // never acquires exclusive lease
    Publish(context.Context, string) (PreparedStoreUpgradeReceipt, error)
    Resume(context.Context, string) (PreparedStoreUpgradeReceipt, error)
    Rollback(context.Context, string) (PreparedStoreUpgradeRun, error)
}

// The payload consumer never selects generic protocol actions itself.
type RehearsalTargetPreparation interface {
    Preview(context.Context, types.PayloadRehearsalConfig) (RehearsalPreparationPlan, error)
    EnsurePrepared(context.Context, types.PayloadRehearsalConfig, types.PayloadRehearsalRunCommand) (RehearsalPreparedTarget, error)
    RollbackPrepared(context.Context, types.PayloadRehearsalConfig) (RehearsalRollbackResult, error)
}
```

`FindActive` is required because the existing `resume` command is bound by
target/config and has no upgrade-run ID before the DB-resident run exists. It
must perform a bounded scan (maximum entry count and one-MiB per journal), reject
symlinks/non-regular files and ambiguity, and match canonical target plus
operation/consumer binding. It must never choose “latest” from two active runs.
Terminal rolled-back runs are not active; starting again creates a new random ID.

The operation-to-recipe registry is fixed at composition. A journal cannot make
the process load arbitrary code. Compaction may continue to acquire one broader
exclusive lease for its existing behavior initially, but both compaction and
migration must delegate orientation/action decisions and every exchange to the
same aggregate/files implementation. A later compaction lock-scope change is
not required by #1645.

## Durable state machine and lock boundary

Keep the existing durable state strings for compaction journal compatibility;
generic names in parentheses clarify their meaning:

```text
planned
  -> copy_intent (candidate-create intent)
  -> candidate_prepared
       `-> copy_retry_intent (durable cleanup/retry authorization)
             |-- remove the exact owned incomplete inode + sync directory
             `-> candidate_prepared (new O_EXCL inode; PreparedAttempt + 1)
  -> copy_complete (recipe build complete)
  -> candidate_sync_intent
  -> candidate_synced
  -> scrub_in_progress (verification intent)
  -> candidate_verified
  -> swap_intent (publish intent, durable before lock acquisition)
  -> swapped
  -> rollback_publish_intent
  -> rollback_ready
  -> committed

rollback_ready|committed
  -> rollback_swap_intent -> rollback_swapped -> rolled_back
```

`copy_retry_intent` is an existing durable compaction state, not a new migration
state. The extracted aggregate preserves the exact string, the transitions
`candidate_prepared -> copy_retry_intent -> candidate_prepared`, and the existing
`PreparedCandidateIdentity`/`PreparedAttempt` append validation. A legacy JSONL
run ending at `copy_retry_intent` observes both legacy possibilities: the old
owned incomplete inode is still present, or an older implementation already
removed and directory-synced it. In the first case recovery removes only the
exact journaled prepared inode and fsyncs the directory; in the second it performs
no deletion. It then creates a new empty candidate and never reuses the removed
inode. New code records `copy_retry_intent` before cleanup, closing the legacy
remove-before-record crash gap. It never removes or adopts an unrecorded inode.

Preparation (`planned` through `candidate_verified`) is cancellable and never
holds the exclusive store lease. Before `swap_intent`, the candidate connection
is checkpointed, closed, its sidecars are absent, the candidate file and
containing directory are fsynced, and verification evidence plus candidate
identity is durable.

Publication appends `swap_intent`, then waits for the exclusive lease using the
caller context. While held it performs only bounded path/link/sidecar and exact
target/candidate identity checks, atomic exchange, the existing constant-count
file/directory sync and no-replace rollback publication, and durable phase
records. It performs no SQLite open, hashing, SQL, checkpoint, copy, migration,
or scrub. Acquisition-to-release is measured against `PublishLockLimit` (at
most one second); a deterministic test makes candidate build exceed one second
while the observed locked interval remains below one second.

If cancellation/deadline occurs before exchange, release and return with the
target unchanged. After exchange may have happened, cancellation cannot skip
durability/reconciliation: use a new bounded recovery context derived from
`context.WithTimeout(context.WithoutCancel(ctx), PublishLockLimit)`, observe
orientation, sync it, and append the furthest proven phase. Syscalls such as
fsync are not preemptible, so elapsed time is evidence as well as a context cap;
an over-limit result is a failed gate requiring fresh-process resume, never a
reason to attempt a second rename blindly.

`swapped` is not rollback authority. At that orientation the original inode is
still at the transient candidate name and the normal recovery obligation is to
append/follow `rollback_publish_intent`, publish that exact inode no-replace at
the durable rollback path, fsync, and reach `rollback_ready`. An explicit
rollback request received in `swapped` or `rollback_publish_intent` invokes that
normal recovery first; the aggregate rejects a direct
`swapped -> rollback_swap_intent` transition. Only `rollback_ready` and
`committed`, with `OrientationRollbackReady`, authorize rollback exchange.

Rollback first closes/checkpoints the rehearsal target and verifies the
canonical rows still match the publication receipt outside the store lease.
It then records intent, acquires the same exclusive lease, observes the two
fenced inodes, exchanges the current prepared target with the immutable original
rollback inode, fsyncs the recovered orientation, and records `rolled_back`.
Rollback intentionally discards rehearsal-only shadow mutations. It must reject
canonical event/audit drift, a changed rollback artifact, an unexpected target
inode, sidecars, an active rehearsal lease, or ambiguous orientation.

## Candidate build, budgets, and cancellation

1. Preview opens the target immutable/read-only, inventories the exact migration
   ledger/catalog, computes the plan digest and canonical evidence, and proves
   target DB/WAL/SHM snapshots unchanged. It creates no candidate.
2. Run rechecks the plan, backup independence, same filesystem, exchange/lease
   capability, safe path/link shape, overflow-safe free-space plan, and journal
   capacity before `copy_intent`.
3. `PrepareCandidate` uses `O_EXCL|O_NOFOLLOW`, mode 0600 (or the validated
   owner-only target mode), fsyncs the empty inode and directory, and journals
   its inode before any copy. Only that inode is cleanup-authorized.
4. Copy the target into the owned inode and verify stable target pre/post
   identity and equal initial file SHA. The configured live store is opened
   only through its existing read-only compatibility path.
5. Execute the exact ordered suffix with the shared migration catalog/executor.
   Candidate migrations may take longer than one second because no published
   target lock is held. `schema_migrations` names/versions must match the plan.
6. Candidate execution uses one connection, WAL autocheckpoint disabled, an
   operation deadline, and resource observation. Check WAL and total owned
   bytes before/after every migration and poll during a long statement so
   context cancellation interrupts it. Reserve the declared maximum plus safety
   margin before building. A WAL/disk overshoot aborts the candidate; it never
   triggers in-place fallback.
7. `wal_checkpoint(TRUNCATE)`, verify checkpoint completion, close all handles,
   reject residual `-wal`, `-shm`, or `-journal`, fsync candidate and directory,
   then perform immutable verification.

The disk plan counts new free-space consumption, not the already-existing
target: independent backup when absent, candidate main-file limit, candidate
WAL limit, owned SQLite temp limit, later rehearsal shadow growth, and safety
margin. Arithmetic is checked before addition. SQLite can write more than one
page between observations; therefore tests must define the permitted polling
granularity/overshoot. If a literal never-exceed byte quota is required, the
portable driver/file-system boundary is insufficient for a single `CREATE
INDEX`; that stronger requirement needs a driver progress hook or filesystem
quota and must not be falsely claimed by post-statement `stat` checks.

## Compatibility and digest contract

`SQLiteCompactionBuilder.VerifyPair` cannot be reused as-is because it requires
equal schema/table digests and a migration intentionally changes both schema and
migration bookkeeping. Reuse its primitives, not its equality assumption.

Both target and candidate independently pass:

1. `VerifyStoreCompatibility` through immutable direct connections;
2. `PRAGMA integrity_check = ok`;
3. zero `foreign_key_check` rows;
4. payload codec scrub for every present payload representation;
5. exact applied migration prefix/name validation and the journaled embedded
   migration-set digest.

Publication equivalence uses the following fully specified
`canonical-event-audit/v1` contract, not `SELECT *` and not all ordinary tables.

### Canonical columns and query order

The `events` logical row contains these columns in exactly this order:

```text
id, kind, agent, session_id, body, created_at, client, workspace, source_hook,
body_original_bytes, body_stored_bytes, body_ingest_truncated,
body_storage_truncated, body_metadata_version, body_availability,
body_pruned_at, body_pruned_plan_id, created_at_norm
```

The `command_audits` logical row contains these columns in exactly this order:

```text
event_id, command_text, input_text, output_text, input_truncated,
output_truncated, exit_code, failed, input_original_bytes,
output_original_bytes, command_wrapper, command_name, failure_reason
```

These are the complete canonical columns present after migration 34. Migration
36 codec metadata and every migration/projection/rehearsal table are deliberately
excluded. Queries use `ORDER BY id COLLATE BINARY` for events and
`ORDER BY event_id COLLATE BINARY` for audits. Primary-key order makes fixtures,
streaming, and diagnostics deterministic; the accumulator below is additionally
order-independent so planner/query-order drift cannot change the result.

### Codec-to-logical-value rules

Before framing, `events.body` and audit `command_text`, `input_text`, and
`output_text` are converted to logical plaintext through the centralized payload
codec adapter:

- a pre-v36 source, or a row for which all five corresponding codec metadata
  fields are SQL NULL, is legacy identity and uses the stored text bytes;
- a fully populated supported identity/zstd tuple is decoded under the existing
  stored/decoded limits, and declared plaintext length, encoded length, format
  version, and SHA-256 must all match;
- partial metadata, unknown codec/version, corrupt compressed bytes, length/hash
  mismatch, invalid UTF-8 plaintext, or an over-limit value fails verification;
- the resulting logical value is always framed as TEXT, so identity and zstd
  encodings of the same plaintext have the same canonical row digest.

Codec metadata is verification input but is never itself included in the
canonical digest. All other columns use their observed SQLite storage class;
declared schema constraints are still checked independently.

### Exact framing and accumulator

All lengths and counters are unsigned 64-bit big-endian. `frame(x)` is
`u64be(len(x)) || x`. A row byte string is:

```text
frame("traceary/canonical-event-audit/v1/row") ||
frame(table_name) ||
for each ordered column: frame(column_name) || encoded_value
```

`encoded_value` is exactly one of:

```text
NULL: 0x00
INTEGER: 0x01 || i64 two's-complement big-endian
TEXT: 0x02 || frame(the exact UTF-8 bytes)
BLOB: 0x03 || frame(the exact bytes)
```

REAL or any driver value outside NULL/int64/string/`[]byte` fails closed; it is
never formatted with `%v`. Text and blob are distinct even with equal bytes.
Each row digest is `SHA-256(row_bytes)`. For each table, stream the rows in the
defined primary-key order while maintaining:

- `count`, an unsigned 64-bit row count;
- `xor[32]`, bytewise XOR of every row digest;
- `sum[4]`, four independent unsigned 64-bit modular sums of the four big-endian
  quarters of every row digest.

The table digest is SHA-256 of:

```text
frame("traceary/canonical-event-audit/v1/table") || frame(table_name) ||
frame(NUL-separated ordered column names) || u64be(count) || xor ||
u64be(sum[0]) || u64be(sum[1]) || u64be(sum[2]) || u64be(sum[3])
```

The final digest is SHA-256 of
`frame("traceary/canonical-event-audit/v1/store")`, then framed `events` table
digest and framed `command_audits` table digest in that order. Evidence carries
the two table counts plus this final lowercase hexadecimal digest. Source and
candidate counts and digest must all be identical. Including primary keys in row
frames and the count/XOR/four-sum accumulator preserves multiset membership and
multiplicity without materializing rows.

Golden tests freeze the column lists, framing bytes, integer sign handling,
NULL/text/blob distinction, and final hashes for empty and mixed fixtures.
Property tests permute row delivery order and require the same accumulator;
mutating any column, type tag, table tag, length, primary key, decoded plaintext,
or multiplicity must change evidence. Identity/zstd pairs with equal decoded
plaintext must match, while corrupt/partial/oversize codec fixtures must fail.

The protected recovery journal stores only the resulting aggregate
counts/digests; it never stores canonical row IDs, payloads, commands, or raw
SQL. Canonical recovery paths and file identities are a separate allowlisted
journal requirement described under security below.

Candidate schema evidence is separate: its schema fingerprint and exact
migration ledger must represent the expected current schema. Treating equal
canonical digest as proof of schema compatibility, or treating a migration
ledger row alone as proof that SQL completed, is forbidden.

The final locked fence uses the journaled target identity (including mode/link
shape and sidecar absence), not an O(database-size) digest. Normal coordinated
mutation is excluded by the lease; any pre-lock SQLite mutation changes the
main identity or produces a sidecar and fails closed.

## Crash/recovery matrix

| Fault boundary | Possible durable observation | Fresh-process action | Safety assertion |
|---|---|---|---|
| Before journal / pre-candidate | original only; no run or `planned` | create/reload plan; no cleanup guess | target unchanged |
| Candidate create before prepared record | unjournaled file may exist | fail closed; never delete an inode whose ownership was not durable | original remains valid |
| `candidate_prepared`, during copy/migration | original + owned incomplete inode | classify by exact inode/plan; append `copy_retry_intent` before cleanup | original unchanged; cleanup authority is durable |
| Durable `copy_retry_intent`, old inode present | original exact + candidate matching recorded prepared inode | remove only that incomplete inode and fsync directory | no arbitrary unlink; crash remains retry-intent |
| Durable `copy_retry_intent`, candidate absent | original exact; rollback absent | treat as already-cleaned legacy/new retry, `O_EXCL` create/sync a new inode, increment attempt, append `candidate_prepared` | retry resumes without reusing old inode |
| Build complete before copy-complete record | original + complete candidate | recipe verifies exact current schema/evidence and records completion, otherwise owned retry | no SQL rerun against target |
| Candidate fsync/checkpoint boundary | candidate may have sidecars or unsynced phase | sidecars/incomplete checkpoint classify incomplete; rebuild; synced candidate can advance after reverify | no publish without closed sidecar-free candidate |
| `candidate_verified` before intent | original + verified candidate | recheck evidence/identities, append intent | target unchanged |
| Durable `swap_intent` before exchange | original at target, candidate ready | acquire exclusive, recheck, exchange once | intent alone never implies exchange |
| Exchange before directory fsync/record | candidate inode at target, original at candidate | derive `swapped` orientation, sync both files/directory, advance; never exchange based only on phase | both valid inodes exist |
| Directory fsync before swapped record | same exchanged orientation | record recovered `swapped`, continue normal rollback publication; do not authorize rollback yet | idempotent recovery |
| `swapped` or rollback no-replace publication boundary | original at candidate or rollback path | normal recovery records/follows `rollback_publish_intent`, finishes rename/sync, and reaches `rollback_ready` | direct swapped-to-rollback is rejected; original is never overwritten |
| Committed record before rehearsal run transaction | current candidate at target + original rollback | `EnsurePrepared` returns bound receipt, then creates run in a separate bounded transaction | resume bridges the handoff gap |
| Rehearsal run pause/resume/scrub | target candidate inode has authorized shadow changes | existing DB checkpoints resume; upgrade journal remains committed | canonical digest remains frozen |
| Rollback intent before exchange | prepared target + original rollback | observe, exchange only if still rollback-ready | no schema downgrade SQL |
| Rollback exchange before sync/record | original inode at target, prepared inode at rollback path | sync recovered orientation and record rolled-back | fresh process never loses both copies |

Every row is exercised with process-level reopen, not only an in-memory fake.
Fault hooks run immediately before/after file creation, file fsync, journal
append/fsync, exchange, no-replace rename, directory fsync, and rollback exchange.

## Migration and rollback path

- Introduce the generic aggregate/interfaces while retaining aliases or mapping
  wrappers for `CompactionRun`, phase constants, CLI DTOs, journal directory,
  and old JSONL records. Golden-load and resume tests include
  `copy_intent -> candidate_prepared -> copy_retry_intent -> candidate_prepared`,
  preserve the exact retry string/attempt semantics, and precede rehearsal wiring.
- Move generic journal/files/orientation logic out of compaction-named ownership;
  leave one implementation. Do not copy these helpers into rehearsal files.
- Add the exact catalog/classifier and migration candidate recipe. Share the
  existing embedded migration executor so normal initialization and candidate
  application cannot drift in version/name/SQL semantics.
- Replace the in-place migration block and `measureRehearsalMigrationWAL` path in
  rehearsal start with `RehearsalTargetPreparation.EnsurePrepared`.
- Keep the existing physical `--backup` artifact as defense in depth and legacy
  rollback compatibility. New prepared runs use atomic orientation rollback;
  legacy runs with no prepared-upgrade receipt continue through the existing
  verified-copy restore path.
- Candidate publication precedes `loadOrCreateRun`; the newly published inode is
  the identity persisted in `payload_rehearsal_runs`. Resume with a committed
  bound receipt and no DB run creates the missing run; otherwise existing resume
  semantics remain strict.
- Rollback of a new prepared run checkpoint/closes the rehearsal DB, verifies
  canonical evidence, performs shared atomic rollback, then verifies the
  original target digest/integrity. It must not also invoke copy restore.

Rollback trigger for the implementation PR: any compaction journal incompatibility,
duplicate exchange implementation, canonical digest drift, unbounded candidate
resource behavior, locked interval above one second in the deterministic test,
or a crash point with unknown orientation. Revert the rehearsal wiring first;
the behavior-preserving shared extraction may remain only if all old compaction
tests/golden journals still pass.

## Behavior tests and TDD plan

| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| Explicit classification | known 35-43 files and one unknown/changed file | plan | exact classes/digest; unknown fails before backup/candidate/target changes | planner |
| Slow offline migration | deterministic migration-35 hook sleeps >1s | run | succeeds; candidate build >1s; exclusive publish interval <1s | integration |
| Target immutability on caps | cancel, wall, WAL, total disk, temp failure at each migration | prepare | target SHA/schema/mode/inode unchanged; only owned candidate may remain | process integration |
| Common compatibility | incompatible target or candidate | verify | each is rejected independently before intent | adapter |
| Canonical equivalence | schema/index/ledger change only | verify | counts/digest equal; altered event or audit fails | verifier/property |
| Concurrent drift | target write, WAL sidecar, hardlink/alias, permission or inode replacement after verification | publish | fails before exchange | filesystem/process |
| Lock coordination | normal shared connection held | publish | waits/cancels without target mutation; after release publishes once | process integration |
| Crash matrix | kill at every listed hook | fresh resume | deterministic orientation; at least one valid target and original always exists | process integration |
| Legacy retry mapping | old JSONL ending at `copy_retry_intent` and after its next `candidate_prepared` | load/resume | exact phases load, attempt increments, and only a new owned inode is built | golden/process |
| Rollback authority | journal/orientation at `swapped`, `rollback_publish_intent`, and `rollback_ready` | request rollback | first two finish normal publication; only rollback-ready starts rollback exchange | aggregate/process |
| Handoff gap | kill after committed publication before run insert | resume | no remigration; run is created once in bounded transaction | integration |
| Full lifecycle | preview, stop after one committed batch, new process resume, scrub, rollback | execute | original target orientation/digest restored; live/source fixture unchanged | end-to-end |
| Compaction compatibility | old JSONL fixtures and each old fault phase | load/resume/rollback | exact fields/phases remain readable; recovery is unchanged except that swapped is safely finalized to rollback-ready before rollback | regression |
| Unsupported platform | Windows build/preview with offline pending migration | prepare | typed unsupported capability before candidate/target mutation | compile/unit |

TDD order:

1. Red tests for generic aggregate transition/orientation parity against every
   existing compaction case, including `copy_retry_intent` golden journals and
   rejection of direct `swapped -> rollback_swap_intent`; extract without losing
   legacy record readability.
2. Red catalog tests for explicit class/body digest/gaps; implement planner.
3. Red candidate cap/cancel/compatibility/canonical-evidence tests; implement the
   offline recipe with no publication wiring.
4. Red publish crash/lock tests; wire the shared publisher only after all
   orientations pass in a fresh process.
5. Red handoff/full-lifecycle/rollback tests; replace rehearsal in-place migration.
6. Run old compaction, rehearsal, full Go, race-relevant process, Linux, and
   Windows compile gates.

Tests assert observable files, schema, digests, state, elapsed lock, and resume
results. They do not assert private helper call order.

## Windows and unsupported-platform behavior

Do not implement atomic publication as `target -> backup; candidate -> target`
or any other two/three-rename sequence. A crash can leave the published name
missing or lose the only rollback orientation. The existing unsupported exchange
adapter remains the fallback: preview reports the capability unavailable, and a
pending offline migration fails before candidate creation or target mutation.
An already-current target may continue the ordinary rehearsal path because no
publication is required.

Provide build-tagged identity/exchange stubs so `GOOS=windows go test` (compile
mode) passes and returns a stable typed unsupported error. Native Windows
publication requires a separately reviewed proof that a primitive such as
`ReplaceFileW` provides equivalent exchange, rollback, directory durability,
open-handle, and crash semantics; it is not part of #1645.

## PR splitting decision

Under the repository's `1 issue = 1 branch = 1 PR` rule, #1645 is one PR and
must not be split across multiple PRs without first creating separate issues.
Use reviewable, revertible commits in this order:

1. behavior-preserving shared aggregate/journal/files extraction plus old-journal
   golden tests;
2. migration catalog and offline candidate recipe/verifier plus cap tests;
3. payload rehearsal preparation/handoff/rollback wiring;
4. crash matrix, long-migration lock evidence, regression and cross-compile tests.

Do not land a commit that routes rehearsal to the new publisher while retaining
the old in-place offline fallback. If the extraction cannot preserve old
compaction journals or the complete crash matrix cannot fit one reviewable PR,
stop before production wiring, create a prerequisite sub-issue for the
behavior-preserving extraction, and update #1645's dependency. Do not silently
violate the one-issue rule.

## Security, quiescence, and residual constraints

- Path-free output applies to public rehearsal evidence, public errors, logs,
  telemetry, and the new redacted status DTO. They contain aggregate counters,
  digests, phases, durations, and fixed error classes only. They never contain
  paths, device/inode values, raw SQL, target rows, event/audit IDs, payloads,
  commands, environment values, credentials, or raw adapter errors. Existing
  compaction CLI fields remain a separately tested compatibility surface; the
  new rehearsal output must not copy its internal journal record verbatim.
- The bounded recovery journal is deliberately protected operational state, not
  public evidence. Its allowlist is: schema version, random run ID, operation,
  canonical target/candidate/rollback paths, phase, target/candidate/rollback
  device/inode/size/mtime/mode/link-count identities, resource plan/observed
  peaks, prepared attempt/identity, migration-set/schema/canonical aggregate
  digests and counts, opaque consumer binding, and UTC timestamps. Raw SQL,
  canonical row IDs/content, commands, payloads, environment, arbitrary errors,
  credentials, and tokens are forbidden even in this journal.
- Journal namespaces are adjacent `.traceary-compaction/` for legacy compaction
  and `.traceary-prepared-upgrades/` for rehearsal migration. Directories are
  mode 0700; JSONL files are mode 0600, regular, single-link, opened no-follow,
  append-only, newline-terminated, and capped at one MiB per run. Active lookup
  also caps entry count and total bytes and rejects ambiguity. Directory and
  record creation/append are fsynced using the existing implementation.
- Journals remain local and are never attached to telemetry, AI review packets,
  or public evidence. Public mapping redacts path and identity fields before
  encoding and converts path-bearing filesystem errors to fixed operation/error
  classes. Debug logging is subject to the same redaction; protected fields are
  read only by recovery/status authorization code.
- Retain an active/committed journal while handoff is incomplete or its rollback
  inode/artifact remains recoverable. Automatic deletion is forbidden. A later
  bounded prune operation may remove only terminal `rolled_back` records, or
  committed records whose explicit rollback-retention policy has expired and
  whose rollback artifact was separately retired; it fsyncs the directory and
  never prunes an ambiguous/corrupt record. #1645 need not add that prune verb.
- Adjacent advisory leases coordinate Traceary processes, not arbitrary
  privileged programs. A non-cooperating writer that already produced a SQLite
  sidecar, lock conflict, inode/mtime/size/mode drift, or link alias fails the
  publish fence. A read-only handle is harmless to content but may affect
  platform rename capability.
- POSIX advisory locks cannot prove the absence of an adversarial process that
  ignores the adjacent lock and races after the last identity check. Do not
  claim otherwise. The operational precondition remains that the explicit
  rehearsal target is offline and owned by this command. If acceptance requires
  proof against an adversarial same-user writer, a process-supervisor/mandatory
  lock capability is required and publication must fail when that proof is
  absent; inode checks alone cannot satisfy it.
- Preserve the configured live store SHA/mode/inode in a quiescent end-to-end
  fixture and prove the workflow never opens that path writable. Runtime SHA
  equality cannot distinguish this command from legitimate concurrent live
  writers, so it is evidence, not an unsafe attempt to freeze the live store.

## Self-review checkpoint

- Core recovery rules remain in an aggregate with explicit state and invariants;
  neither the payload use case nor the large SQLite adapter owns a procedural
  rename state machine.
- Interfaces are consumer-oriented commands/receipts. No `dryRun`, `resume`, or
  `allowMutation` boolean controls side effects, and raw SQL does not cross the
  infrastructure boundary.
- Candidate verification deliberately separates format compatibility, expected
  schema/migration evidence, and canonical data equivalence.
- The shared abstraction is justified by two existing recipes and does not
  generalize arbitrary migration engines or storage providers.
- Windows fails before mutation rather than weakening atomicity.
- The two non-portable limits are explicit: exact instantaneous WAL quota during
  one SQLite statement, and proof against a process that ignores advisory locks.
  Tests and release evidence must not overstate either guarantee.
