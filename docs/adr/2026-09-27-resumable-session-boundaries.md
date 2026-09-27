# ADR: Session identity without session lifecycle management

[日本語](./2026-09-27-resumable-session-boundaries.ja.md)

- Status: Proposed
- Date: 2026-09-27
- Decision owner: Traceary maintainer (human checkpoint)
- Reviewers: architecture, context/handoff, hook delivery, execution, storage/bundle, independent verifier roles
- Related issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Supersedes: the earlier C/passive-close/runtime-lifecycle proposal in this same design PR, recorded through commit `f502b939`.

The user approved the new direction in conversation: Traceary tracks AI audit logs, events, and refinements; a Session is a grouping identity, not a lifecycle-managed object.
This record replaces the earlier recommendation rather than extending it with runtime monitoring.
Detailed implementation and public-contract transitions remain Proposed.
The human maintainer owns Ready and merge through GitHub UI; no automated transition is authorized.
This PR changes bilingual design documentation only, uses `Refs #2394`, and leaves the issue open.

## Requirement summary

Append and retrieve relevant recorded work by identity without requiring an active, nonterminal, or recently started Session.
Remove lifecycle dependence from context/handoff selection and formatting, ordinary host closure, and synthetic stale closure.
Keep identity, source AI, Workspace, parent delegation lineage, labels, model metadata, recorded events, and refinement coverage.
Execution success/failure/timeout/signal remains meaningful, but belongs to an invocation outcome, not Session status.
No reopen, generation, runtime availability, `RuntimeEpisode`, or continuation entity is needed merely to accept resumed logs.
Physical deletion of legacy columns is not a requirement for achieving these semantics.

Non-goals: runtime monitoring, uptime measurement, retroactive history rewriting, automatic deletion of nonempty logs, production operation, install/login, or release execution in this design task.

## Current behavior and evidence

The current public behavior does include lifecycle-derived statuses; this design changes those dependencies rather than claiming they do not exist.
At the reviewed repository baseline:

- `handoff.go:126` emits `STATUS`.
- `context_pack_builder.go:74-75,335-342` applies a stale-start eligibility window.
- `output.go:127-144` defines the currently unused `sessionSummaryOutput` DTO.
- Session `Active`/`Latest` return an Event; they are not a context summary consumer contract.
- Legacy active queries can include `ended_with_late_events`.
- `update_stale_sessions` has no `runtime_mode` restriction; long-idle one-shot work can be terminalized before finalization.
- Bundle import rejects a manifest schema version newer than the store and then rejects unknown tables.

Current interactive Claude, Gemini, and Kimi `SessionEnd` invoke aggregate `End`.
Current passive mappings include Codex `SessionEnd`/`Interrupt`, Claude `StopFailure`, and Kimi `Interrupt`.
Interactive logical IDs generally use native `session_id`, except within a one-shot wrapper; repeated starts can succeed idempotently and later same-ID events can attach to terminal records.
These are distinct current mappings, not a claim that all hosts have identical hook coverage.
The new proposal removes ordinary tracked-session terminalization instead of protecting a Session terminal invariant.

[#2393](https://github.com/duck8823/traceary/issues/2393) pins acquired SID, absolute database route, and raw cwd for its scoped passive paths.
Fully resolved Workspace is not pinned: `TRACEARY_WORKSPACE` and repository detection can re-resolve during replay.
That known routing gap is independent of lifecycle removal; neither change proves all legacy routing is fixed.
Native IDs are correlation data, not authentication or authorization scope.
Host identity ambiguities require verified namespace/store routing, not invented identities or automatic cross-store merges.

## Alternatives considered

| Option | Benefit | Cost | Recommendation |
| --- | --- | --- | --- |
| Retain lifecycle as authoritative | Minimizes semantic change | Continued stale/terminal read exclusions and host-driven state coupling conflict with log tracking | Reject as target; legacy wire compatibility only |
| Separate optional runtime episodes | Could describe runtime instances with stronger host evidence | Adds monitoring concepts, identity/order requirements and schema work unrelated to accepting logs | Do not introduce for this requirement |
| Session as log grouping identity only | Matches append/retrieve/refine purpose; execution outcomes remain separate | Requires caller audit, public-contract migration and execution isolation | Recommend, subject to implementation checkpoint |

## Conceptual model

| Concept | State / behavior | Invariant |
| --- | --- | --- |
| Session identity | Groups recorded work and metadata | Appendability/retrievability does not depend on `endedAt`, active/stale status or start age |
| Event | Recorded audit/content fact with identity and provenance | Preserve useful history, redaction and reliable delivery; do not fabricate current state |
| Refinement | Summary plus explicit coverage | Coverage remains monotonic; preserve existing consumers without requiring a new event-stream assembler |
| Context selection | Explicit identity/selector or latest relevant recorded session | Relevance and explicit filters govern selection, not terminal state |
| Invocation / ExecutionResult | One-shot process completion with success/failure/timeout/signal and usage | Execution owner owns completion/idempotency; later logs cannot overwrite outcome |
| Parent lineage | Delegation relationship | Parent completion does not deny child append; lineage is not process cancellation |
| Legacy lifecycle fields | Historical compatibility data | Retained, not ordinary eligibility authority; typed one-shot outcomes use an isolated adapter |

An Invocation is a conceptual responsibility, not an approved new table or API schema.
The compatibility implementation retains current one-shot mode/end/reason storage with its dedicated wrapper SID binding; optional additive invocation storage needs a separate approved migration plan.
Historical `runtime_mode=one_shot` identifies a run row, not the actor that wrote its terminal reason.
Preserve its exact stored typed reason as recorded historical result/legacy metadata through an isolated read-time compatibility adapter, without backfill or rewriting.
Project a confirmed supervisor outcome only with reliable supervisor-finalization/provenance evidence.
If an old host/direct/parent-cascade close may have preceded finalization, expose unknown actual outcome alongside the recorded reason and provenance limitation; never assert actual success from the reason alone.
Old first-reason reconciliation retains the first reason and rejects contradictions; it does not prove the writer.
The exact evidence-recognition contract belongs to the implementation checkpoint, not a new table or blanket heuristic here.
Interactive End also writes success: a success reason without one-shot provenance is not an execution outcome; unknown stays unknown.
Later appended logs cannot erase or override recorded historical results or confirmed outcomes.
Keep the current `session run` behavior: each invocation creates a dedicated new SID.
The acquired wrapper SID can identify the execution through an explicit immutable 1:1 compatibility binding; delivery receipts remain distinct from execution identity.
No new Invocation ID/table is needed now. Multiple invocations attached to one conversation are not requested and require a separate future identity/migration design.
Move the old first-terminal invariant from Session to the execution owner where completion is meaningful.
Do not erase or recompute an execution result when later Session events arrive.

## Responsibilities and consumer-oriented interfaces

| Owner | Responsibility / consumer boundary | Must not do |
| --- | --- | --- |
| Domain Session/identity | Identity, source AI, Workspace and delegation metadata | Authorize logging or reading based on lifecycle |
| Domain execution owner | Completion and immutable invocation outcome | Treat outcome as Session closure or deny later logs |
| Application event/refinement writes | Append useful records, preserve coverage and delivery idempotency | Reject resumed logs because of historical end/GC markers |
| Application context query | Return existing relevant context/refinement with current filters, MemoryAsOf and bounds | Route context through `Active`/`Latest` merely to obtain a lifecycle snapshot |
| Presentation context/handoff | Render selected content and recorded provenance | Add `STATUS`, lifecycle-derived duration or runtime availability; rewrite human summary prose |
| Presentation host adapter | Individually interpret callbacks; acquire fixed SID/DB routing before enqueue; Workspace pinning remains a target/gap | Infer closure from inactivity or discard logs during cleanup |
| Application invocation / supervisor | Own process completion, cancellation, usage and wrapper routing | Confuse removal of recursive Session End with removal of subprocess cancellation |
| Infrastructure | Persist compatible legacy data, implement bounded retrieval and reliable replay | Re-enable read exclusions from old end/import data; silently migrate schema |

Use narrow consumer contracts: append to an acquired identity; select existing context using identity/relevance, current coverage consumers, filters and bounds; finalize an invocation using its outcome.
Audit callers of existing `Active`/`Latest` Event-returning interfaces before adding or replacing a method.
Include `List ActiveOnly`, `FindEndedSessionIDs`, parent inference, doctor stale diagnostics/fixes and hook-local end markers in the lifecycle-gate audit.
Do not broaden inferred parent selection when removing active/stale eligibility; preserve evidenced spawn lineage and report unknown when evidence is insufficient.
Do not introduce a broad lifecycle facade or a generic monitoring framework.
Remove unused `sessionSummaryOutput` and derived `SessionStatus` propagation only after a caller audit proves the affected paths and any public output compatibility obligations.
A SessionSummary may still carry content aggregates and coverage; removing an unused DTO does not remove all summary types.
Public list/status output is not assumed nonexistent; each affected public consumer needs an explicit transition decision.

## Context and handoff contract

Explicit identity selection wins over implicit latest selection.
Implicit selection chooses the latest relevant recorded session in the requested source/workspace scope.
Preserve existing latest-selection scope, order and tie-breaking wherever independent of lifecycle; remove only active/stale eligibility.
Any ranking change to prefer meaningful content over registration-only records requires a separate follow-up checkpoint, not a silent change bundled here.
Phase 1 is removal-only: preserve actual existing selection/order, fields, refinement/compact behavior, recent-command limits and memory-count budgets.
Do not require a new generic context assembly engine.
Current handoff as-of is `MemoryAsOf`, not an event cutoff; preserve that actual scope.
Summary coverage is already recorded, but general uncovered-event inclusion and event-as-of/token/time budgets are not all current handoff features.
Any coverage-bounded event stream, event cutoff or new token/time framework is a separate future feature only if needed.
A session older than 24 hours remains eligible for explicit lookup; `startedAt` age alone cannot reject it.
Remove stale-triggered handoff re-query and guidance to close work with `session end`, not only the stale rejection.
Preserve explicit date/content filters where supported and the existing memory as-of cutoff: timestamps are data, not lifecycle boundaries.
Deliberately remove generated `STATUS`, lifecycle-derived duration and running/stopped assertions from handoff/context with versioned release documentation, golden output tests and a JSON/downstream field audit.
Inventory `--allow-stale` and stale-after consumers; the recommended transition is documented deprecated no-op compatibility flags once lifecycle eligibility is removed, subject to public version policy.
Do not edit a human-authored summary that happens to contain those words.
Preserve existing refinement coverage and historical notes without introducing a new uncovered-event handoff stream in this change.

## Host callback and delivery contract

Ordinary tracked-session `SessionEnd` stops terminalizing the Session and its descendants.
It may still flush usage/transcript records, emit bounded diagnostics, and clean hook state using acquired SID and fixed DB routing.
Flush and replay must not silently discard logs when hook state is cleaned or a callback arrives late.
Audit hook-local end markers: an already-existing start can succeed without clearing a marker, and Stop stale-marker handling can clear state or suppress flush/extract/routing fallback.
Remove semantic end gates while preserving callback/delivery dedup through scoped receipts; do not disable dedup wholesale.
Propose ceasing default inferred host-close content notes for end-only empty payloads: that is noise reduction, not blanket event removal.
Retain useful Interrupt/StopFailure audit outcomes when needed, with accurate provenance and no labels pretending to describe Session state.
Retain existing start/end/close events as history; no retroactive purge or body rewrite.

A minimal alternative for operational receipts is existing delivery ledger/spool metadata plus bounded sanitized diagnostics, not a new event framework.
If a durable local receipt ID or acquisition `received_at` is needed for commit-before-spool-clear deduplication, approve its exact storage separately.
Host redelivery without an event ID remains distinguishable from neither a new host occurrence nor runtime order by inference alone.
A local receipt can deduplicate local replay without establishing a host episode.
Spool `CreatedAt` is not persisted event `recorded_at`; neither proves runtime duration.
Preserve redaction, acquisition routing, retry reliability and useful failure logs.

| Host | Required individually evidenced contract review |
| --- | --- |
| Claude | Replace ordinary interactive SessionEnd terminalization; keep necessary flush/cleanup and useful StopFailure outcomes |
| Codex | Review removal of default inferred SessionEnd notes; retain useful Interrupt outcomes and flush/cleanup |
| Gemini | Replace ordinary interactive SessionEnd terminalization; verify start/clear and flush/cleanup independently |
| Grok | Verify actual supported hooks and acquisition; absence of callback is not a synthetic end |
| Kimi | Replace ordinary interactive SessionEnd terminalization; retain useful Interrupt outcomes |
| Muse | Verify actual supported capture/receipts; do not fabricate lifecycle callbacks |
| Antigravity | Verify actual supported capture/flush/cleanup; do not fabricate lifecycle callbacks |

This table prescribes future contract checks, not certified host coverage or executed live tests.

## One-shot, explicit end, and housekeeping

Preserve one-shot CLI exit code, process cancellation, signals, timeout behavior, usage capture, fixed wrapper SID/DB routing, and active-execution protection.
Session remains appendable after the process completes.
Disabling recursive Session End does not cancel the supervisor's responsibility to stop child processes.
Execution-result isolation must ensure legacy end imports or replay cannot override an execution outcome.
Before or atomically with phase 2, protect one-shot rows from GC/doctor fixes, parent cascade (`FindOpenChildSessionIDs` currently lacks a mode filter), host end, direct `session end`, and outcome-changing import/replay.
Scoped compatibility import may restore recorded old results, but may not overwrite a current result or promote ambiguous provenance to confirmation.
Only supervisor `FinalizeOneShot` may write an execution result; leave its current finalization and first-result reconciliation intact until a proven atomic replacement.
For explicit `session end` against one-shot work, return an actionable refusal without changing logs/outcome, not fake success.
Stop automatically overwriting content refinements with the current `cli:session-finalize` text “one-shot process finished: reason”; outcome belongs to its execution record/projection.
Historical generated refinements remain unchanged; any display policy is separate.

Do not silently reinterpret the current public `session end` command.
Choose the target: `session start` registers identity and a start marker idempotently; `session end` records an explicit end marker only, without terminalization or a retrieval gate.
Deprecate lifecycle interpretation with versioned release documentation and a bounded compatibility adapter; preserve existing ID output and flags where feasible, validated by contract tests.
Until the stage is approved, the bridge retains old behavior; no immediate idempotence or compatibility guarantee is made.
The public-contract migration checkpoint precedes implementation of the new marker semantics.
The existing End use case also bundles summary/refinement and cascade; its marker-only replacement must preserve supplied summary and coverage semantics while removing terminal side effects.
During transition, an old end marker may remain recorded in legacy fields/events, but it must not exclude subsequent content or mutate an isolated invocation outcome.
Exact warning, exit-code, structured-output and marker details must be resolved within that chosen transition.

Disable synthetic stale close: GC must not invent `endedAt` as a housekeeping action.
Separate opt-in, bounded retention/empty-orphan housekeeping from log identity.
Protect delegation lineage, refinement references/coverage, pending spool/receipts and active one-shot execution; identify safe orphan criteria and require a bounded dry-run before any deletion proposal.
Housekeeping eligibility is not determined merely by a non-ended status.
Never delete nonempty log records merely because they are old or marked ended.
Any broader retention deletion requires separate explicit authorization/design; it is not part of this proposal.

## Behavioral specification and TDD plan

These are proposed acceptance tests, not tests run by this documentation-only change.

| Scenario | Observable target | Test level |
| --- | --- | --- |
| Same native identity resumes after explicit or GC end marker, then appends | Same grouping identity, old markers retained, new event queryable; no reopen/generation needed | Hook + query integration |
| Duplicate start/end deliveries | No loss of useful logs or duplicate completion; preserve documented receipt scope | Delivery integration |
| Missing host event ID | Do not invent occurrence/order; preserve local replay reliability and redaction | Spool integration |
| Idle session started over 24 hours ago selected explicitly | Relevant events/refinement returned without stale-start denial | Context integration |
| Historical refinement coverage and close notes | Existing coverage/content behavior preserved; no purge or required new event stream | Context query |
| Current distinct runs create dedicated SIDs and retry independently | Immutable wrapper binding and per-run first result/reconciliation retained; later same-SID log remains queryable | Invocation integration |
| Historical one-shot typed outcome versus interactive success/legacy_unknown | Recorded typed reason preserved; confirmed outcome requires supervisor evidence; unknown remains unknown, no backfill or later-log override | Compatibility query |
| Legacy one-shot host/cascade prematurely records success, then finalization attempts failure | Raw stored success remains exportable; contradiction does not make it verified success; actual outcome is unknown absent reliable provenance | Compatibility + finalization |
| Host/direct end, GC/doctor, parent cascade or import/replay reaches one-shot row | Atomic guard protects outcome; direct end refuses actionably; supervisor finalization remains intact | Use case + SQLite |
| Resume after local end marker, then end/Stop | Flush/extract, fixed routing and cleanup work without loss; receipt dedup still works | Hook fixtures |
| Host-closed sibling and newer unrelated Session exist when parent inference runs | Only evidenced spawn/lineage is accepted; insufficient evidence yields unknown, not nearest active/unended Session | Hook parent fixture |
| Implicit latest with only old unended records, or old-unended/new-ended mixture | All remain eligible, preserving actual existing deterministic order and proven parent inference | Query integration |
| Explicit end supplies summary/refinement | Marker-only target preserves summary and coverage, not recursive terminal side effects | Use case + CLI |
| One-shot success/failure/timeout/signal, then later Session event | CLI/process outcome preserved, event appended, no completion conflict | Supervisor + use case |
| Parent execution finishes, child emits later log | Child lineage and content retained; process supervision rules remain independent | Domain + integration |
| Host end callback has useful usage/transcript data | Data flushed with acquired SID/DB, bounded diagnostics and cleanup; no terminalization or silent drop | Host fixtures |
| End-only empty callback | No default inferred close content note; operational receipt reliability remains | Host + delivery |
| Legacy bundle imported with end/runtime fields, no schema change | History preserved; fields do not restore read exclusions or override new execution outcome | Bundle integration |
| Existing command/memory limits, compact behavior and MemoryAsOf apply | Actual existing bounds/fields preserved; no generated STATUS/availability/duration; no invented event cutoff | Query + CLI |
| Human summary contains status prose | Summary unchanged; only generated lifecycle scaffolding removed | Rendering |
| GC sees idle nonempty record or active execution | No synthetic close/nonempty purge; execution and references protected | Storage integration |
| Each of seven host integrations changes contract | Host-specific fixtures prove flush, routing, cleanup, useful failures and receipt behavior | Adapter integration |

| TDD step | Red | Minimal green | Refactor boundary |
| --- | --- | --- | --- |
| Read dependence first | End/stale-start rejects relevant context or produces STATUS | Existing scoped/order-preserving query without lifecycle gates; remove generated lifecycle rendering | Consumer query versus old Event-returning helpers |
| Ordinary closure writers | Host callback or GC creates authoritative terminal state | Preserve flush/diagnostics/cleanup without closure; no default empty close note | Host interpretation versus useful event capture |
| Execution isolation | Session append/import/replay conflicts with one-shot result | Completion belongs to execution owner; preserve process outcome and active protection | Invocation outcome versus Session identity |
| Compatibility | Old bundle/end marker restores exclusion or loses history | Legacy wire retained; Session eligibility read nonauthoritative, recorded results preserved and confirmed-outcome provenance explicit; public adapter tested | Storage compatibility versus product semantics |

Run affected unit, CLI output, context, SQLite, hook/spool and supervisor tests at implementation checkpoints.
Any schema phase additionally requires migration, index, bundle compatibility and recovery tests.
Independent review and fresh CI are required before delivery; documentation checks alone do not validate these behaviors.

## Human checkpoint and proposed delivery phases

The conversation approved the direction, not every public transition or migration detail.
Before implementation, approve existing context selection preservation/output migration, explicit-end deprecation, host contract changes, execution protection and compatibility boundaries.
Use one ticket, branch and PR per bounded implementation phase after that checkpoint; these are proposals, not authorization to create tickets now.

1. Audit callers, remove context/read lifecycle dependence and unused status DTO propagation, retaining legacy wire/storage.
2. Before or atomically with stopping closure writers, complete caller audit and migrate parent inference, Active/List ActiveOnly/FindEndedSessionIDs, doctor stale diagnostics/fixes and hook-local end gates. Only proven spawn/lineage may infer parent; insufficient evidence is unknown, not the nearest active/unended candidate. Doctor --fix must not remain a synthetic closure writer. Install atomic one-shot guards for GC/doctor, parent cascade, host/direct end and import/replay while retaining FinalizeOneShot reconciliation; then remove ordinary closure writers and synthetic GC close; review each host's flush/cleanup, useful outcomes and empty-note policy; design only opt-in safe housekeeping.
3. Isolate one-shot outcomes using the current dedicated wrapper SID and typed compatibility storage; stop generated outcome refinements. No shared-session invocations or new identity/table are required; any optional additive storage needs a separate approved design.
4. Consider optional physical lifecycle-column removal only in a separate decision; it is not necessary for this feature.

Dogfood before release using isolated DB/store fixtures and sanitized controlled real sessions to exercise resume, flush, refinement, replay and one-shot outcomes.
Isolate DB/store, `TRACEARY_HOOK_STATE_DIR`, spool/queue roots, receipts, GC markers, leases, diagnostics and usage offsets as applicable, and verify overrides are actually honored before dogfood.
Define fixture routing and redaction in advance; leave authentication untouched, never use private logs as default input, and do not change production host configuration.
The design task performs no install, login, production operation or release.

## Migration and rollback safety

Initially retain `endedAt`, `runtimeMode`, `terminalReason` and their existing bundle wire representations without DROP, backfill or historical mutation.
Legacy fields are not ordinary Session status authority; historical typed reasons remain recorded metadata, and confirmed supervisor outcomes require reliable provenance through the isolated execution compatibility adapter.
Preserve existing wire fields and recorded timestamps initially; date diagnostics, explicitly scoped retention inputs and content filters are not blanket-removed with lifecycle-derived context scaffolding.
Read changes precede removal of ordinary closure writers and GC behavior; execution-result projection follows as an isolated phase.
Mixed-version imports/replay of old end data must not silently restore exclusion or replace new outcomes in the new implementation.
Retained legacy boundary events/columns support old bundle representation, but cross-version roundtrip semantics are not universally guaranteed.
Document intentional new-contract incompatibilities and version gates.
Old binaries do not implement these semantics; mixed-version operation is not a semantic guarantee.

Without a schema migration, application rollback is technically possible but not automatically safe.
An old binary's first GC/doctor run can synthesize closes across newly unended records and stale reads can reject them.
Require freezing old closure writers or using an isolated recovery copy plus explicit operational acceptance; no existing freeze flag is claimed. Preserve all recorded data.
If choosing a feature/config rollback switch, define its affected read/writer scope and compatibility behavior at checkpoint; no such current flag is claimed.
Future Invocation schema/index changes require versioned migration and bundle gates; older builds reject newer bundle versions.
No universal downgrade promise is made, even for additive SQL changes.
Retain a tested export/recovery path and logical records; never rewrite historical markers to simulate a successful rollback.

Rollback triggers include lost resumed events, broken refinement coverage, changed process outcomes, wrong-store replay, or incompatible public output without migration.
Monitor bounded sanitized delivery/query failures, not runtime uptime or raw transcripts.
Workspace replay pinning remains a separately scoped gap throughout.

## Unresolved implementation decisions and self-review

- Exact latest-relevant ranking, source namespace/collision handling, context output migration and public list/status consumers.
- Explicit-end deprecation schedule and marker/output compatibility details.
- Per-host evidence for flush/cleanup and useful Interrupt/StopFailure retention; exact minimal receipt storage if existing ledger is insufficient.
- Dedicated-run SID binding, atomic active-execution protection and typed compatibility adapter limits; optional shared-session invocation identity/storage is a separate future decision.
- Opt-in orphan criteria, reference/lineage/spool protection and any separately authorized retention scope.
- Whether a rollback switch is needed and its exact scope; versioned recovery if Invocation storage is introduced.

The model removes Session lifecycle ownership rather than relocating it to runtime episodes.
It preserves execution responsibility, useful audit facts, identity routing and refinement coverage.
Behavior tests observe content, outcomes and compatibility, not internal call order.
No schema changes, source tests or live-host validation are claimed by this documentation PR.

## References

- [Issue #2394](https://github.com/duck8823/traceary/issues/2394)
- [Scoped passive acquisition #2393](https://github.com/duck8823/traceary/issues/2393)
- [Architecture principles](../architecture/README.md)
- [Event lifecycle (current behavior)](../lifecycle.md)
- [Storage model (current behavior)](../storage/README.md)
