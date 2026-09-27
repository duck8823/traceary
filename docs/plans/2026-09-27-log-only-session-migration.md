# Log-only Session migration implementation plan

[日本語](./2026-09-27-log-only-session-migration.ja.md)

- Status: Proposed work packages; scheduling/acceptance-ready, not implementation-ready.
- Date: 2026-09-27
- Design: [Session identity ADR](../adr/2026-09-27-resumable-session-boundaries.md)
- Design issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Design PR: [#2398](https://github.com/duck8823/traceary/pull/2398), `Refs #2394`, not a closing reference.
- Baseline: ADR commit `3df81e1881fcdfb77c3417e0d95f0a73a0a065a1`.

## Authority and implementation checkpoint

The approved direction is Session as log grouping identity, not lifecycle management.
P1–P4 below are proposed package identifiers, not created GitHub issues.
After the prerequisite checkpoint, each package gets one ticket, one dedicated branch and one PR.
Each eventual issue body must carry its concrete positive/negative acceptance, ownership, dependencies and validation gates, not merely a package title.
Human architectural Ready/merge decisions remain in GitHub UI.
This planning artifact does not itself authorize runtime, schema, database or runtime-ticket/PR operations; main separately owns publication of these design documents.

Before implementation, the maintainer must accept:

- Public handoff/JSON downstream impact and flag transition policy, including actual supported flags and version boundaries.
- Exact supervisor-provenance recognition and atomic writer guard contracts; no stored success reason alone proves an actual outcome.
- All closure-dependent consumers, local end markers and supported host callback contracts, including parent-inference constraints.
- Explicit start/end marker, supplied summary/coverage, warning/return, internal API and legacy CLI alias contracts.
- Scope of isolated dogfood and recovery evidence, including degraded/unavailable-host decisions.

This plan provides sequencing and acceptance criteria; the detailed audits above still block implementation readiness.
At the last supplied observation, design CI for `3df81e18` was in progress; this document does not assert CI success.

## Scope and sequencing

```text
checkpoint -> P1 (read-only consumer migration)
           -> P2 (execution guards and legacy result adapter)
           -> P3 (atomic ordinary producer/consumer cutover)
           -> P4 (integrated validation, dogfood and recovery)
```

Dependencies serialize P1 → P2 → P3 → P4 because they share session, hook, CLI and SQLite sources.
Readonly fixture inventory and independent review may run alongside implementation; never assign overlapping writers.
S/M/L are relative effort sizes, not calendar promises.

| Package | Size | Risk | Merge prerequisite |
| --- | --- | --- | --- |
| P1 | M | HIGH: public context/handoff output | Public-contract checkpoint |
| P2 | L | HIGH: execution outcome and repository guards | P1 plus provenance/writer-path audit |
| P3 | L | HIGH: multi-host and public lifecycle cutover | P2 guards proven, consumer/host checkpoint |
| P4 | M | MEDIUM for evidence/docs; HIGH if cross-version behavior must change | Integrated P1–P3 head |

Non-goals: new runtime monitoring, shared-conversation Invocation IDs/tables, physical lifecycle-column removal, retention purge, generic context assembly/token-budget framework, install/login, production configuration changes, or release publication.
Keep acquired SID/store routing; fully resolved Workspace replay pinning remains a separate known gap.

## P1: Remove lifecycle gates from context and handoff

**Ownership:** context/handoff consumer code in `presentation/cli/`, relevant `application/queryservice/` and `application/usecase/` query/build paths, their existing tests and user-facing docs.
Existing entrypoints include `presentation/cli/handoff.go`, `presentation/cli/context_command.go`, and `presentation/cli/output.go`.
Do not change shared parent-selection `Active` semantics in this package.

**Work:**

- Inventory stale-start rejection, handoff stale re-query and generated `STATUS`/close guidance; remove lifecycle eligibility and scaffolding.
- Preserve latest-selection scope/order/tie fallback, existing fields, compact/refinement behavior, human prose and supported explicit filters.
- Keep `MemoryAsOf` memory-only, recent-command/memory-count limits and recorded coverage semantics; do not add event-as-of or generic uncovered-event streams.
- Audit unused `sessionSummaryOutput` and `SessionStatus` consumers before removal; retain content aggregates/coverage summary types.
- Inventory actual context stale flags. Recommend deprecated no-op compatibility flags for one transition period, with dates/version policy separately approved.
- Warn on stderr only when a deprecated flag is explicitly supplied; do not contaminate stdout JSON/ID output.

**Positive acceptance:** explicit lookup returns old/unended and legacy-ended relevant content; implicit latest across old-unended/new-ended records preserves deterministic existing ordering; generated lifecycle scaffolding disappears through a documented output transition.
**Negative acceptance:** no replacement running/availability status, no user-summary rewrite, no ranking change preferring new content over registration-only records, no parent-inference widening.

**Validation:** affected `presentation/cli`, `application/queryservice` and `application/usecase` tests; handoff golden/JSON consumer tests, stale-start fixtures, compact/refinement and MemoryAsOf regressions.
**Rollback trigger:** missing selected content or unapproved downstream output breakage.
**Recovery:** retain compatible storage and fixture exports; reverting read behavior restores stale restrictions, so require explicit operational acceptance rather than claiming transparent rollback.

## P2: Protect supervisor outcomes and preserve legacy execution results

**Ownership:** session execution/finalization in `presentation/cli/`, `application/usecase/`, `domain/model/`, repository contracts and `infrastructure/sqlite/`; hook/doctor callers only where guards require them.
No ordinary-session closure removal yet.

**Work:**

- Audit every result writer and evidence source before defining confirmed-supervisor projection.
- Guard one-shot rows atomically against GC/doctor, parent cascade, host/direct end and outcome-changing import/replay; only supervisor `FinalizeOneShot` can write the current result.
- Direct `session end` against one-shot returns actionable non-success with no log/outcome mutation.
- Keep dedicated new SID per run, immutable wrapper binding, existing finalization/first-result reconciliation, exit code, cancellation, timeout, signals, usage and fixed routing.
- Same-result retries remain idempotent; contradictory results do not overwrite the first record.
- Preserve raw historical mode/end/reason. One-shot mode identifies a run, not the actor that wrote its result.
- Confirm actual supervisor outcome only from reliable provenance; premature host/cascade success followed by attempted failure stays recorded metadata with unknown actual outcome if evidence is insufficient.
- Scoped legacy import restores historical records without overriding a current result or inventing provenance; no backfill or timestamp heuristics.
- Stop new `cli:session-finalize` outcome-text content refinements; preserve historical generated refinements and user summaries.

**Positive acceptance:** dedicated-run retries and later same-SID logs work; historical raw values remain exportable; supervisor process behavior and recorded outcomes survive concurrent races.
**Negative acceptance:** no inferred successful run from interactive success or ambiguous one-shot reason; no new Invocation table/ID, no historical rewrite, no cleanup-induced loss of result.

**Validation:** use-case/domain/SQLite and CLI supervisor tests; concurrent finalization, child-parent end, direct end, GC/doctor, import/replay, premature legacy success and conflicting failure fixtures.
**Rollback trigger:** changed process exit/outcome, bypassable guard, lost historical value or result/log conflict.
**Recovery:** retain current typed compatibility columns and dedicated wrapper binding; restore only on an isolated copy or with unsafe old writers frozen under an approved procedure.
P3 cannot proceed until the guard transition is proven atomic.

## P3: Replace ordinary session closure with log-only boundary capture

**Ownership:** ordinary session domain/use cases, SQLite query/update consumers, public session CLI, doctor and all actual supported host adapters/assets in `presentation/cli/` and integration packages; associated docs/tests.
P2 execution protection and finalization remain intact.

**Atomic prerequisite and work:**

- Migrate parent inference, `Active`, `List ActiveOnly`, `FindEndedSessionIDs`, doctor stale diagnostics/fixes, local end markers and closure writers before or in the same cutover.
- Do not let unended-record growth broaden inferred parents. Accept only proven spawn/lineage; insufficient evidence is unknown, not nearest active/unended Session.
- Doctor fix must not remain a synthetic stale-close writer; no GC-generated `endedAt`.
- Target `session start`: idempotent identity/start-marker registration with consistent metadata. Checkpoint defines conflicting metadata, internal API/alias choice and dedup scope.
- Target ordinary `session end`: explicit marker plus supplied summary/refinement coverage, no terminal gate/cascade. Preserve output IDs/flags where feasible; approve exact return/warning migration first.
- Remove local semantic end gates, including already-existing starts not clearing markers and Stop stale-marker cleanup paths, without disabling scoped receipt dedup.
- Individually verify Claude, Codex, Gemini, Grok, Kimi, Muse and Antigravity against actual supported callbacks; do not invent absent hooks.
- Preserve useful usage/transcript flush, redaction, acquired SID/fixed store routing, receipts and cleanup.
- Stop default empty inferred close content notes; retain correctly scoped useful Interrupt/StopFailure outcomes and all historical boundary events.

**Positive acceptance:** same-ID resume after old explicit/GC marker appends and retrieves; resume → end/Stop flushes and cleans safely; end with summary preserves coverage; sibling/newer unrelated records cannot become a guessed parent.
**Negative acceptance:** no ordinary recursive terminalization, stale closure, nonempty-log purge, schema DROP or blanket dedup disablement; no removal of supervisor subprocess cancellation.

**Validation:** host-specific fixtures, CLI start/end contracts, query/domain/SQLite, parent lineage, hook-local state, spool/replay, transcript/usage and redaction tests for each supported host contract.
**Rollback trigger:** lost log/flush, wrong parent/store, guard regression or mixed consumer/writer semantics.
**Recovery:** keep legacy wire/storage and boundary history; use an isolated copy or an explicitly approved freeze procedure. Old GC/doctor may mass-close new unended records and old readers may reject stale content.
A partial producer-only rollback/cutover is not acceptable.

## P4: Validate migration and isolated recovery

**Ownership:** migration/recovery docs, controlled fixture/evidence definitions, existing test and wave tooling. Runtime fixes discovered here require classification and the owning package scope, not incidental unreviewed edits.

Evidence/fixture preparation can start earlier as readonly work; P4 completion and dogfood bind to the merged integrated runtime head.

**Work:**

- Verify legacy columns, boundary events, raw execution metadata and refinement coverage across bundle export/import; newer schema manifests are rejected by older builds.
- No universal roundtrip/downgrade promise; document old-reader restrictions and GC/doctor mutation risk.
- Prepare bounded dry-run and isolated recovery-copy evidence; no retention deletion is enabled by status changes.
- Run controlled dogfood on the merged integrated runtime head before release, not on this documentation head.
- Isolate DB/store, `TRACEARY_HOOK_STATE_DIR`, spool/queue/receipts, GC markers, leases, diagnostics and usage offsets where applicable; verify actual supported overrides are honored before testing.
- Leave authentication and production host configuration untouched; use sanitized controlled sessions, never raw private logs.
- Record SHA, UTC time, provider CLI version, controlled scenario, validated commands and sanitized results.

Record a capability/evidence matrix for all seven hosts, with separate fixture/live results: `PASS`, `FAIL`, or `NOT_RUN`, actual hook support and reason.
Unsupported hooks are not failures to implement fictional callbacks and are not a pass; unavailable-host degradation requires an explicit maintainer decision.
Unverified required acceptance blocks release.

**Positive acceptance:** integrated runtime tests, fresh CI, wave evidence, required controlled scenarios and isolated recovery all pass on their bound SHA.
**Negative acceptance:** no install/auth/production mutation, no release publish, no unchecked override accidentally touching real stores, no docs-only checks counted as runtime validation.
**Recovery:** stop dogfood on any isolation failure; preserve sanitized evidence and recover from the isolated copy, not production mutation.

## Validation gates and completion evidence

At implementation commits, run staged affected tests via `scripts/test-select-staged.sh`, affected lint/typecheck/tests as applicable, and documentation checks when docs change.
At each integrated runtime wave, run:

```sh
scripts/run-wave-e2e.sh --wave ID --ref SHA --project-dir WORKTREE --evidence-dir DIR
```

Bind wave logs to the integrated SHA; unrun/failed evidence is not success.
Required fresh CI and independent review remain mandatory; reuse validation evidence only for proven identical inputs.
Resolve reviewer findings through main triage, not automatic blanket fixes.
P4 is complete only with actual required scenario evidence and accepted degraded-host decisions, not elapsed-time targets.
This planning change ran only documentation validation; no source/runtime/host tests or release gate are claimed.

## References

- [Session identity ADR](../adr/2026-09-27-resumable-session-boundaries.md)
- [Staged validation strategy](../../CONTRIBUTING.md#staged-test-strategy)
- [Storage model](../storage/README.md)
