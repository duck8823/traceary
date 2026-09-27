# ADR: Resumable logical sessions and host-close observations

[日本語](./2026-09-27-resumable-session-boundaries.ja.md)

- Status: Proposed
- Date: 2026-09-27
- Decision owner: Traceary maintainer (human approval required)
- Reviewers: architecture, lifecycle, hook-delivery, storage/bundle, and independent verifier roles
- Related issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Checkpoint: design discussion only; neither runtime implementation nor acceptance is authorized by this ADR.

## Requirement summary

A host can stop presenting a conversation without ending the logical work recorded by Traceary.
The design must distinguish host shutdown from logical termination without weakening terminal-state, ownership, retry, or delegation guarantees.
The recommendation is option C now: preserve the monotonic logical `Session`, retain passive host-close observations, and report runtime availability as unknown.
The semantic direction is separate logical conversation and runtime concepts, never reopening a terminal aggregate.
Option B is a conditional future realization of that separation, not an approved implementation or a prerequisite for eventual human approval of this design.
C combines the #2393 passive observation baseline with a proposed future read contract; it does not convert every host SessionEnd to passive behavior.
Existing interactive Claude, Gemini, and Kimi `SessionEnd` mappings invoke aggregate `End` and remain unchanged.
The identified passive mappings are Codex `SessionEnd`/`Interrupt`, Claude `StopFailure`, and Kimi `Interrupt`; there is no universal passive host-close mapping.
The distinction applies to identified resumable host-close callbacks; future reclassification needs evidence and review.
A common monotonic invariant does not imply identical hook mappings.
No runtime code, schema, historical data, or existing `active` contract changes in this design PR.
Issue #2394 stays open; a draft design PR uses `Refs #2394`, not a closing reference.

## Context and evidence

The [official Codex hook contract](https://developers.openai.com/codex/hooks), checked on 2026-09-27, describes main-thread `SessionEnd` on normal close, archive/delete of an open conversation, or 30 minutes idle with no connected client; not on subagents.
Its reason is currently `other`.
`SessionStart` sources include `startup`, `resume`, `clear`, and `compact`; compaction can continue the same turn.
These signals do not prove a distinct runtime instance.
This is documentation evidence, not a live authenticated host test.

Repository evidence at base `a61b0a18b57345f90009ac449a2be3210c8e0910`:

- `domain/model/session_lifecycle.go` preserves the first terminal transition.
- `application/usecase/session_usecase_impl.go` returns a start Event from `Active` and ends descendants through `End`.
- `infrastructure/sqlite/sql/find_active_session.sql` includes ended sessions with later events in legacy activity selection.
- Hook-delivery semantic fingerprints do not establish runtime generation identity.
- Bundle import rejects `manifest.BundleSchemaVersion` newer than the store before its unknown-table rejection; adding a table is not automatically backward compatible.

The passive adapters addressed by [#2393](https://github.com/duck8823/traceary/issues/2393) pin acquired SID, absolute database route, and raw cwd, not a fully resolved Workspace.
`TRACEARY_WORKSPACE` and repository detection can still resolve Workspace again during replay.
Fully immutable Workspace binding is a target and known gap, not an achieved baseline; this scope also does not prove that every legacy routing path is fixed.

Interactive hook logical IDs equal native `session_id`, except inside a one-shot wrapper.
`SessionStart` for an existing ID succeeds idempotently and writes hook state; subsequent same-ID events attach to the already terminal record as late events, not a continuation or reopen.
C temporarily preserves this behavior: it does not suppress or drop later events, and legacy `active` can include `ended_with_late_events`.
Passive close notes recorded after end can themselves satisfy that activity query.

The `session_start`/`session_end` delivery fallback uses `session_id`, so repeated starts can collapse into exact retries; start source is not instance identity.
Passive close receipts without native event IDs are preserved differently: this asymmetry cannot establish runtime order.
Host redelivery ambiguity is distinct from Traceary local replay after commit-before-spool-clear.
A future locally assigned receipt ID and acquisition `received_at` can deduplicate local replay without proving a host episode or causal order.
Current spool `CreatedAt` is not equivalent to persisted event `recorded_at`, and neither establishes runtime duration.
The existing one-shot wrapper can observe its local process lifetime; that does not prove host-global episode identity.

## Cross-host applicability

The same logical terminal invariant applies to every host; callback completeness and correlation evidence differ.
Native/runtime ID strings establish correlation only, not authentication or authorization scope.

| Host | Capability boundary under C |
| --- | --- |
| Claude | Interactive `SessionEnd` invokes `End`; `StopFailure` is passive; preserve both mappings |
| Codex | `SessionEnd`/`Interrupt` are passive; documented callbacks do not prove runtime instance/order identity |
| Gemini | Interactive `SessionEnd` invokes `End`; missing runtime-instance evidence means unknown, not fabricated closure |
| Grok | Missing or incomplete end evidence means unknown, not fabricated closure |
| Kimi | Interactive `SessionEnd` invokes `End`; `Interrupt` is passive; do not infer stronger correlation |
| Muse | Missing or incomplete end evidence means unknown, not fabricated closure |
| Antigravity | Missing or incomplete end evidence means unknown, not fabricated closure |

This table is a design constraint, not a claim of tested hook coverage across all clients.

## Alternatives considered

| Option | Benefit | Cost or invalid assumption | Recommendation |
| --- | --- | --- | --- |
| A: clear `endedAt` and reopen a Session | Reuses the existing entity | Violates first-terminal preservation; conflicts with one-shot ownership and retry-ledger expectations; late old End can terminalize a resumed generation | Reject |
| B: separate `RuntimeEpisode` under a logical Session | Could represent proven host instances without changing logical termination | Requires stable instance/correlation identity, replay identity and causal ordering, concurrent-client rules, schema and bundle design | Conditional future only |
| C: logical Session plus passive host observations | Preserves existing lifecycle and records only known facts | Cannot provide runtime availability, duration, or episode counts | Recommend now, subject to human approval |

Episodes must never be represented as child Sessions.
A child means delegated work, with recursive terminalization and GC implications; a runtime restart does not establish delegation.

## Conceptual model and invariants

| Concept | State and behavior | Constraint |
| --- | --- | --- |
| Logical `Session` | Existing aggregate lifecycle; explicit end remains terminal | First terminal time/reason is preserved; no reopen |
| Host-close observation | A recorded host report, associated with an acquired logical identity | Does not end the aggregate, descendants, or a one-shot owner |
| Legacy activity selection | Existing `active`, `ended_with_late_events`, and stale/activity rules | Activity is not aggregate terminal status or runtime availability |
| Runtime availability | `UNKNOWN` with current evidence | Close receipt does not prove the present host is stopped |
| Future `RuntimeEpisode` | Optional host instance projection | Requires independently proven correlation and replay/order identity |
| Future continuation relation | Explicit link between logical work records | Separate approved design; not a child and not an automatic resume binding |

1. Keep three read concepts separate: aggregate logical terminal status, legacy activity query, and runtime availability.
2. Describe the semantic meaning as the **last recorded host-close observation**; the final CLI/API wording is provisional. Recording time is not occurrence time, causal order, duration, or an episode count.
3. Without a host event ID, repeated receipt and distinct close occurrences can be indistinguishable. A semantic fingerprint cannot resolve that ambiguity.
4. Replay order and recorded-at timestamps cannot establish runtime causal order.
5. Target: bind native identity, fully resolved Workspace/local root, and a fixed database route at acquisition, before spooling. #2393 currently pins SID, absolute database route, and raw cwd only; full Workspace binding remains a gap. Any future episode identity must also be bound before spooling, not rediscovered during replay.
6. A late old close must not end a newer generation. Without proven generation identity, do not select a guessed current episode.
7. Preserve explicit end, one-shot completion, descendant terminalization, and existing GC `legacy_unknown` termination. GC can still terminalize a logical root; this limitation is not silently changed. Its SQL is not restricted by `runtime_mode`, so an unprotected long-idle one-shot can be terminalized and conflict with `FinalizeOneShot`. Owner-only completion is the intended boundary, with this known GC exception.
8. A resume after logical termination cannot reopen that record or automatically bind a new continuation. Archive, delete, and ordinary close share insufficient reasons; restore cannot infer a continuation.
9. Multiple clients sharing a native thread do not imply one current runtime episode.

## Responsibilities and proposed interfaces

These are semantic contracts, not approved API names or DTO schemas.
Keep host payloads and SQLite details outside the domain.

| Layer / owner | Responsibility and boundary | Failure / non-owner contract |
| --- | --- | --- |
| Domain | Own logical terminal invariants; define observation meaning; own a future episode invariant only if approved | Never infer lifecycle from host DTOs or storage order |
| Application write | Accept normalized passive observation with acquired logical identity; coordinate existing event recording without `End` | Missing/ambiguous identity cannot mutate a guessed Session; no automatic continuation |
| Application read/query | Expose terminal status, legacy activity, and observation summary as distinct read concepts | Runtime availability stays unknown; no silent `active` redefinition |
| Presentation / host adapter | Parse host source/reason; bind native ID and local root to logical identity before enqueue | Unsupported or incomplete host evidence is explicit, not guessed |
| Presentation / CLI | Label recorded observations and unknown availability without an uptime claim | Preserve existing flags/output contracts unless separately approved |
| Infrastructure | Persist/replay acquired SID/database/raw-cwd context; target fully immutable Workspace binding; implement repositories and optional future projection storage | Do not rebind acquired SID/database; Workspace may currently re-resolve, a known gap; never guess an episode |

Use-case comparison: existing session use cases own aggregate writes (`End`, `FinalizeOneShot`); `Active` is a read returning a start Event, not an aggregate operation.
Observation capture is a different semantic operation: reusing aggregate termination would introduce recursive side effects into a passive report.
Reuse event recording where suitable; do not create a generic host lifecycle engine, Strategy hierarchy, or a new use-case class for every hook name.
Whether an observation needs a separate public method remains a checkpoint decision based on consumers and transaction boundaries.

## Behavioral acceptance specification

The following are proposed tests for subsequent approved implementation, not tests executed by this documentation change.

| Given / when | Observable result | Level |
| --- | --- | --- |
| Open logical work, passive Codex close/resume/close/resume on the same native thread | Logical record stays nonterminal; observations do not establish episode count or availability | Hook integration + read |
| Codex `SessionStart` source `compact` or `clear` | No inferred new runtime instance; no logical reopen | Adapter |
| Open Codex thread is archived or deleted, then restored | Passive close remains a report; reason `other` cannot distinguish causes; no inferred continuation | Integration |
| All clients detached and idle; close is delivered later | No claim that recording time equals shutdown time, or that the host remains unavailable | Read |
| Same event ID is replayed | Existing delivery idempotency prevents duplicate effect within its documented scope; logical state unchanged | Delivery integration |
| Missing event ID, identical payload arrives twice | Cannot assert one occurrence or two episodes; preserve documented receipt/dedup semantics | Delivery + read |
| Old close replayed after a newer resume | Fixed old routing retained; no termination or guessed current-episode update | Spool integration |
| Passive Codex/Kimi Interrupt, then resume | Interrupt is not logical termination or proven runtime boundary | Adapter + domain |
| Concurrent clients use one native thread | Do not collapse them into a single guessed episode; availability unknown | Concurrency integration |
| One-shot command nests passive host callbacks and receives close, excluding the known GC exception | Owner completion boundary retained; passive close does not complete it or descendants | Use case |
| Terminal Session receives same-ID start/resume then an event | Same logical ID and first end preserved; event retained as late event; legacy active remains eligible, not a continuation | Hook + read |
| Terminal Session receives a passive close note | End unchanged; note retained and can count as a late event in existing activity query | Hook + read |
| Commit succeeds but spool clear fails, then local replay | Distinguish local duplicate from host redelivery; future receipt identity may dedup replay without episode/order claims | Delivery integration |
| Terminal parent receives close or resume | First terminal preserved; no reopen, descendant recreation, or automatic continuation | Domain + use case |
| Explicit end recursively terminalizes descendants | Existing behavior retained; observations cannot undo it | Use case |
| Unprotected long-idle one-shot encounters GC before finalization | Characterize current GC terminalization/FinalizeOneShot conflict; owner-only completion is intended, not a current GC guarantee | Storage + use case |
| GC closes stale logical root with `legacy_unknown` | Existing terminal retained even on later resume; expose limitation, not hidden semantics change | Storage integration |
| Existing bundle exported/imported under C | Schema and logical/event records remain compatible; no fictional episodes | Bundle integration |
| Future B bundle opened by older importer | Defined version gate/rejection tested before rollout; unknown tables must not be silently lost | Compatibility |
| Future proven episode identity is missing or conflicts | No projection onto a guessed episode; retain observation and surface uncertainty/error policy | Future B integration |

## TDD plan

| Step | Red specification | Minimal green | Refactor boundary |
| --- | --- | --- | --- |
| 1 | Identified passive callback changes aggregate/descendant terminal state | Route passive reports without aggregate `End` | Domain invariants versus application capture |
| 2 | Replay resolves a different session/store | Bind and replay acquired routing context | Presentation acquisition versus infrastructure transport |
| 3 | Read output implies running/stopped, duration, or episode count | Render separate terminal/activity/recorded-observation concepts and unknown availability | Query DTO versus CLI rendering |
| 4 | Explicit end, one-shot, GC, or bundle regression | Preserve their existing observable behavior | Avoid lifecycle flags in generic event recording |
| Future B only | Proven identity/order or format compatibility fails | Add narrowly scoped identity/projection contracts after approval | Episode invariant owner versus persistence |

Run affected unit, SQLite/spool/bundle integration, host fixture, and CLI contract tests before each implementation commit.
Fresh CI and independent review remain implementation gates; a passing documentation check is not lifecycle validation.

## Human checkpoint and delivery phases

The human maintainer must first decide whether C is acceptable, resolve the product questions below, and approve the implementation scope.
Independent architecture/lifecycle, delivery, and bundle reviewers should check invariants and evidence before that checkpoint.
This ADR remains Proposed until that explicit decision is recorded.
This high-risk ADR draft must not be automatically marked Ready or merged; the human maintainer owns those transitions through the GitHub UI.

After approval, create independent tickets and one PR per ticket:

1. Treat completed #2393 passive acquisition/routing as the baseline, not a new implementation ticket. If review finds a remaining routing gap, scope a separate ticket to that proven gap; do not claim a global routing fix.
2. Add or clarify observation/read contracts and user-facing uncertainty, without redefining existing `active`.
3. Add regression fixtures and operator documentation for explicit end, resume-after-terminal, GC, and bundles.
4. Consider B only after a host evidence spike proves stable instance/correlation and replay/order identity, followed by another human-gated design and versioned migration/bundle plan.

These phases are proposals, not preauthorized external issue creation or implementation.

## Consequences, migration, and rollback

C keeps the current schema and logical lifecycle unchanged.
It cannot answer whether a runtime is currently attached or how many times it restarted; observation wording must communicate that cost.
Existing explicit terminal records and GC results remain terminal, even when that limits later resumability.

No historical episode backfill is permitted from close/start timestamps, fingerprints, or archive/restore guesses.
C read contracts use existing events without migration; if approval identifies any required schema change, apply the same checkpoint and versioned bundle gate as B.
For future B, use additive storage only after a bundle-format compatibility gate, explicit unknown historical state, and tested upgrade/old-import behavior.
An additive SQL migration alone does not solve the importer rejection contract.

Rollback C by disabling a newly introduced observation projection or adapter mapping while retaining logical records and recorded facts.
Rollback future B by disabling its projection, retaining logical records and recoverable episode data, and using the approved bundle recovery/export path.
Neither rollback may clear `endedAt`, convert episodes into child Sessions, or fabricate continuation links.
Trigger rollback on wrong-route replay, terminal-state mutation, misleading availability, or bundle data loss; test the rollback path before release.
Monitor bounded counts of routing failures, replay conflicts, and unknown identities without collecting credentials or raw transcript data.

## Unresolved product decisions

- Is unknown runtime availability acceptable, or must the host expose stronger evidence before this feature ships?
- Should recorded-close summaries be visible by default, and what stable CLI/API labels and timestamp provenance will they use?
- Which documented receipt/dedup behavior should users see when host event IDs are absent, and should local receipt identity be added?
- Should passive observations be excluded from activity? That requires a separate public-contract change, not a hidden C filter.
- Should GC exclude one-shot work, use dormancy, or use a different reason? These are separate human decisions, not changes authorized here.
- How should users explicitly proceed after GC or explicit logical termination? A continuation relation requires a separate approved design.
- If B becomes feasible, is an episode per client or per host-defined runtime instance, and what closes it under concurrent clients?
- Which host instance, event identity, and ordering contracts are demonstrably stable? Current thread ID and start/end reasons are insufficient.
- What versioned bundle format and downgrade/recovery policy would make B safe?

## Self-review and validation scope

The recommendation preserves a domain-owned terminal invariant and separates transport reports from aggregate lifecycle.
Tests specify observable state and output, not private call order.
Unknown identity/order and legacy GC limitations remain explicit rather than hidden behind a new abstraction.
This PR changes bilingual design documentation only; documentation pairing and removed-alias checks plus `git diff --check` validate the artifact, not a live host or runtime implementation.

## References

- [Issue #2394](https://github.com/duck8823/traceary/issues/2394)
- [Passive acquisition/routing work #2393](https://github.com/duck8823/traceary/issues/2393)
- [Codex hook contract](https://developers.openai.com/codex/hooks)
- [Architecture principles](../architecture/README.md)
- [Event lifecycle](../lifecycle.md)
- [Storage model](../storage/README.md)
