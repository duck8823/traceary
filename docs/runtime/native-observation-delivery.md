# Native observation delivery design checkpoint

[日本語](native-observation-delivery.ja.md)

Status: **Proposed, not accepted or implemented**. This is the before-code design input for [the observation-delivery leaf](https://github.com/duck8823/traceary/issues/2466), not a shipped CLI command reference or native-host certification. The [accepted headless R0](../adr/2026-11-03-headless-runtime-r0.md) remains authoritative. Independent High/security review and main-session adoption must resolve the open contract decisions before production implementation.

## Requirement and existing boundaries

Existing native hook capture must continue to work without a daemon. Optional live delivery carries derived, redacted metadata only; it cannot carry prompt, code, audit or memory bodies, credentials, terminal input, approval decisions, stop requests or resume instructions. Successful queue admission does not authenticate native origin or establish input readiness.

The current `ObservationMetadata` and `Daemon.Observe` ports in `application/runtime/daemon.go` are untrusted metadata transport. Existing owner-private Unix transport and launch identities are reusable boundaries, not proof that a native hook belongs to a run. `launchEnvironment` in `infrastructure/runtime/pty_unix.go` strips ambient runtime variables and supplies run/generation/epoch hints; matching those values, cwd or a native session ID alone must not create a bound observation.

The earlier [observation ADR](../adr/2026-10-18-desktop-runtime-observations.md#launch-and-binding) proposes per-invocation native hook arguments carrying a descriptor path. It does **not** establish universal native support or adopt a host configuration mechanism. Each host/version must independently demonstrate unique propagation and non-destructive hook composition. A descriptor path is correlation data, not a capability granting input or process control. Unsupported propagation leaves observations unbound and native interaction unknown; a wrapper or cwd/PID fallback is not allowed.

## Conceptual model and responsibility

| Concept | Owner | Invariant |
| --- | --- | --- |
| Derived observation | Domain value | Closed, bounded metadata with no body or effect arm; native source claims remain untrusted |
| Managed binding descriptor | Runtime metadata adapter and immutable domain binding | Exact store/worktree/profile host/version/run/generation/epoch and physical source witnesses; never saved-PID authority |
| Native hook context | Existing host CLI adapter | Consume the native payload once; preserve original capture, receipt/spool and stdout/exit decisions |
| Optional delivery | Application port | One total monotonic deadline, bounded in-flight work, no replay or primary-result override |
| Local transport | Existing daemon adapter | Observations endpoint only; no daemon auto-start or management operation |
| Native causal evidence | Versioned adapter and reducer | Descriptor correlation alone cannot reconcile native state or approval visibility |

Domain code owns identity and bounds, not JSON/files/sockets. Application code orchestrates the optional delivery without formatting host decisions or accessing SQL directly. Infrastructure owns no-follow descriptor reads, root locks, atomic publication and wire deadlines. Existing CLI hooks remain responsible for primary capture and host stdout/exit formatting. This adds a capability to the existing object-level services, not a new class per hook or another target Registry.

## Proposed delivery sequence

The existing hook handler reads its payload once and preserves its primary capture and decision result. An optional path derives an allowlisted metadata value and resolves a supported, explicitly supplied native invocation context. It then uses the same bounded application delivery port as a future explicit metadata intake. No raw native payload is forwarded and no second stdin consumer is introduced.

The proposed **250 ms total monotonic deadline** starts before optional descriptor validation and includes root/file validation, connect, handshake, frame write and acknowledgement. Separate 250 ms phase budgets would exceed this contract and are rejected. Operations that cannot finish within the budget return unavailable/unknown telemetry without delaying an unbounded background worker or changing the original capture result. The bound must also cover cancellation and retained in-flight ownership; a callback that ignores cancellation cannot create unlimited replacement workers.

No daemon, unsafe sockets, oversized frames, queue pressure, disconnect and timeout must preserve the original event IDs, inserted/redelivery behavior, hook stdout, exit code and existing receipt/spool ownership. Live delivery does not create a daemon-specific spool or drain worker. An observation acknowledgement means admission only, not a committed event, native delivery, approval, process state or permission.

## Implemented history baseline

The [history leaf](history-reconciliation.md) is merged in `main`, including bounded metadata audit/preview/forget. This proposal must extend that actual implementation rather than defer inventory compatibility or treat own-produced descriptors as unsupported metadata. History leaf completion does not certify lifecycle hardware, native hooks or session recovery.

## Open contract decisions

1. **Public intake schema.** The accepted R0 fixes the additive command name as `traceary runtime observe`. Its flags and closed versioned metadata-only schema still require before-code design review; this document does not advertise executable flags or a shipped command.
2. **Descriptor representation.** Compare per-run immutable private files with a bounded closed envelope keyed by full launch selection. Decide owner publication, no-follow race checks, maximum sizes, aggregate budget and generation retirement before implementation. Do not silently treat ambient variables as an equivalent descriptor.
3. **Unique native association.** Require independently verified per-invocation propagation plus an exact existing launch binding. Native session IDs need a unique versioned association; first-match or same-cwd matching is not sufficient. Ambiguous, foreign, unsupported or stale contexts cannot become bound/current.
4. **Metadata loss and retention.** A new descriptor format must extend the actual typed audit/export/forget and aggregate-budget contracts. Define whether recovery links are erased and which audit/policy values survive. Every partial write prefix must remain readable by the actual launch and lifecycle readers; normal own-produced roots cannot become permanently unsupported. Unknown versions still fail closed.
5. **Hook composition.** Preserve unrelated global and per-run handlers, host decision precedence and one logical capture. Do not install or overwrite global settings, change auth/trust/model/PATH, or introduce a wrapper. Synthetic merge fixtures remain fixtures, not proof of real host/version behavior.

These decisions are linked: choosing a new descriptor file without its lifetime, audit and erase contract would defer a required safety property rather than solve observation delivery. No representation is adopted by this document.

## Behavior tests and validation plan

| Given / action | Required observation |
| --- | --- |
| Supported unique invocation descriptor and a generated private Unix server | Exactly one bounded derived frame; original capture IDs/counts/stdout/exit unchanged; ACK is admission only |
| Missing, slow, unsafe or backpressured daemon at each phase | One total deadline; no primary failure, second drainer, automatic replay or unbounded worker growth |
| Env/native-ID/cwd-only claim, raced/future/foreign descriptor, wrong store/host/version/generation/epoch or ambiguous association | Unbound/rejected optional telemetry, no native authority and no primary-result change |
| Duplicate and out-of-order metadata, delayed callback from an old generation | No current-generation promotion; causal validation remains a separate versioned boundary |
| Global and per-run hook composition fixtures | Existing handlers/decision precedence remain intact and logical capture is not duplicated; unverified native composition stays unknown |
| Descriptor publication/retirement/forget fails at any write boundary | Actual metadata readers accept every committed prefix; loss/retention and uncertainty are explicit, with no automatic retry |

Start with RED/GREEN observable tests and CLI compatibility goldens. Use real generated private sockets for elapsed deadline and queue tests; do not widen production deadlines to make fixtures pass. Run affected staged/reverse tests, race checks for concurrent paths, build/vet/lint, paired-document checks and fresh exact-head CI/security. Final independent implementation review and the integrated daemon wave remain required. Synthetic fixtures cannot certify real native hooks, physical sleep/wake or same-conversation resume.

## Rollback and scope

Disable optional live delivery only. Preserve native hook capture, decisions, existing spool replay, core history, Registry and native conversations. No implicit input, approval, replay, resume or new-chat fallback is introduced. Authentication and billing stay with the actual host CLI. UI/framework/distribution are outside this leaf; Muse native work remains deferred. This proposal neither completes the leaf nor authorizes production changes before its design checkpoint.

## Descriptor representation review candidate

Also compare reusing the full immutable launch snapshot in the existing owner-private `execution.json` as the descriptor body, resolved through explicit full launch selection. This is a candidate, not adopted. It may avoid extra per-run files or envelopes, but actual source and tests must establish that the snapshot retains every store/worktree/profile host/version and run/generation/epoch witness and can be read safely within the same bounded deadline. Descriptor reference or equality alone never certifies native binding. Independently verified per-invocation hook propagation, causality and versioned host evidence remain necessary. Any change to the existing schema semantics must also update the typed metadata audit/forget/budget and valid-write-prefix contract.

## Independent before-code checkpoint outcome

The independent High/security review of fixed tree `7a94503a` returned **CHANGES_REQUIRED**, not implementation or native certification approval. The actual descriptor candidate file is `execution.json` (`executionEnvelope.launches`). Do not adopt new per-run files; refine reuse of the existing full launch snapshot and explicit full selection. Without a new file or schema, no inventory extension is needed, but actual tests must prove unchanged metadata audit/forget and valid-write-prefix behavior.

Before-code work remains: define unambiguous wire worktree/profile/full selection, the production bounded observation receiver's responsibility boundary with the later reducer, a closed event-kind set, and delivery placement within the primary hook budget. Do not guess a domain meaning for `ObservationMetadata.TargetID`; define explicit rejection of new schemas by old daemons. Descriptor equality never promotes native binding, input authority or approval. The reviewer's proposed extra ring buffer is not automatically adopted: first ensure it would not create a second state owner alongside the later projection.

Optional delivery must follow primary capture and be skipped when it would consume existing packaged-hook headroom or spool-drain reservation. Preserve stdout/exit and receipt/spool semantics. Recheck the concrete command flags, wire schema and receiver boundary at the corrected checkpoint before adding production code. Per-host native propagation/composition certification remains in adapter leaves; no universal support is asserted here.

## Concrete v1 contract for corrected before-code review

This section supersedes the earlier open representation/schema alternatives for the corrected checkpoint; it is not an implementation or native certification claim. It stays within accepted R0, introduces no host configuration installation, and does not accept product-wide architecture changes.

### Descriptor and receiver ownership

Use the immutable full launch snapshot already owned by `application/runtime.Runtime` and persisted in `execution.json`. The explicit selection is exactly the existing `OutputSelection`: epoch/run/generation/worktree/profile. The client never opens a descriptor file. The runtime must match all five selection components, profile host/version and a live, successfully published owned run in this epoch. A saved record, reserved/exited/uncertain run, a persisted PID, or matching environment/native IDs cannot satisfy this check. Validate the original target with existing `TargetRegistry.Revalidate` outside `Runtime.mu`, then recheck the same owned-run identity/state before admission. No file/Registry lookup runs while holding the owner mutex. Unsupported native propagation still yields unbound telemetry, not native evidence.

The new selected intake is owned by the existing Runtime, not the constructor's optional legacy `ObservationReceiver`. It validates before admission and again at consumption, using the existing single daemon observation queue and worker. Production composition already supplies a RuntimeFactory, so the selected intake must work with the real main composition; legacy `observe` with nil receiver remains explicitly unsupported. No second queue, drainer or per-epoch ring is added. Retain at most one bounded last-arrival, **untrusted** sample per live owned run, inside that run's owner, at most 256 samples of 4 KiB each (1 MiB total). Clear it on actual Wait completion, uncertainty, owner shutdown or epoch loss. Replaced/duplicate/out-of-order samples never promote native state and never change input readiness or the core Session. The later reducer may extend this same owner boundary; it must not replay this last-arrival sample as fresh causal evidence.

### Wire schema and version skew

Keep outer protocol version 1 and legacy `observe` unchanged. Add a distinct operation `observe_selected_v1` and one request arm `selected_observation`; never fall back to legacy observe. The closed selected schema is `{selection:{epoch,run,generation,worktree,profile}, metadata:{host,host_version,event_id,kind,native_session_id?,native_turn_id?}}`. The operation name is its version discriminator; do not add a redundant nested version field. Every required field must be present; null, duplicate keys, trailing JSON, unknown fields and mixed arms are invalid. All protocol-1 frames retain the existing non-null/closed-field contract. An unsupported operation (for example observe_selected_v2 with otherwise valid protocol-1 fields) returns unsupported; unknown fields, null or malformed JSON remain invalid. A future operation is never parsed as selected-v1 or replayed as legacy observe. Do not weaken or special-case the common outer decoder. Selection uses canonical domain IDs, not a guessed `target_id`; do not send `TargetID` in the new schema. Host/version must equal the pinned profile. Kinds are exactly `prompt_boundary`, `tool_boundary`, `turn_boundary`, `session_boundary`. Event/native IDs are bounded correlation identifiers, not raw payload/body fields. The encoded selected value is at most 4 KiB, inside the existing 64 KiB frame. Older daemons reject the unknown arm/operation; newer clients report unavailable/unsupported, never replay under another schema.

Only the observations endpoint accepts it. Status advertises `observe_selected_v1` only when a live runtime intake exists; this capability is not native-host certification. The correlated successful ACK is `queued`, with no data/effect/approval arm. It means admission only, not consumption, core commit, native association or visible wait. Validate the entire one-of request and reject mixed response arms across existing SDKs, including the separate lifecycle branch when integrated. Queue pressure preserves existing frame/byte caps and degraded status. Error mapping reuses fixed payload-free invalid/unsupported/changed/not_found/rejected/busy codes; no reply includes submitted identifiers or bodies.

### Operator and hook surface

Public `traceary runtime observe` takes no stdin and no positional arguments. Required flags are `--runtime-dir` (explicit absolute existing root), `--epoch`, `--run`, `--generation`, `--worktree`, `--profile`, `--host`, `--host-version`, `--event-id`, `--kind`; optional native-session/native-turn ID flags are correlation only. No default root, environment selection or native association is inferred. Unknown/malformed flags are operator misuse and nonzero; valid optional delivery outcomes print one closed JSON object `{schema_version:1,outcome,binding:"unbound",authority:"admission_only"}` with zero exit. Outcomes are queued/unavailable/unsafe/unsupported/busy/rejected/changed/not_found/invalid/timeout. No elapsed time is an authority input, no automatic daemon/core bootstrap occurs, and output-write failure remains a CLI error.

Add explicitly opt-in `--observe-runtime-dir`, `--observe-epoch`, `--observe-run`, `--observe-generation`, `--observe-worktree`, `--observe-profile`, `--observe-host-version` to the existing hidden hook group. These flags supply correlation context only; missing/partial/invalid context silently skips optional telemetry without breaking the original hook. Derive host and boundary kind only from the existing supported hook client/action, event correlation from the already acquired spool receipt, and native IDs only from the already consumed payload's supported allowlisted fields. Do not add another stdin consumer. Unsupported clients/actions skip delivery. No flag changes packaged global/per-run settings, argv policy, auth, model or PATH; actual versioned native propagation/composition remains in host leaves.

Run optional delivery only after successful primary capture **and** current spool clearance, before opportunistic backlog drain. The existing no-spool fallback may deliver after the primary succeeds, without creating a new spool. Deferred/failed capture, replayed backlog, and failed spool clear never trigger new optional delivery. Before starting, require remaining packaged-hook time greater than 250 ms plus the existing drain reserve; otherwise skip. The optional path writes nothing to hook stdout/stderr and cannot replace the primary return value, receipt or event IDs.

### Total deadline and failure containment

The single 250 ms monotonic budget starts before optional metadata derivation/root validation and covers connect, handshake, write and ACK. Do not start another full budget per phase. Use the existing bounded transport worker slots; retained work keeps its slot until actual exit. The hook's optional derivation+delivery additionally has one retained slot per process/service, panic containment, immutable borrowed input and cancellation checks. A callback ignoring cancellation cannot spawn unlimited replacements or delay primary completion beyond the caller budget. No automatic retry, delayed background replay or durable observation spool. Server target revalidation is outside its owner lock under a bounded context; existing connection/queue/worker caps remain effective even when a dependency ignores cancellation.

### Proof and rollback

RED/GREEN: real main-like composition accepts a matching selected sample and actual owner API can inspect it; nil legacy receiver remains unsupported for legacy messages. Test each selection/profile/host/version/state mismatch and physical target exchange, dequeue-time exit/epoch invalidation, bounded latest samples and cleanup without changing core Session or control state. Test new/old mixed-version sockets, every known request/response arm, future operation/null/duplicate/unknown cases, real phase deadlines, blocked callbacks with retained slot, queue pressure, panic/cancel and no replay. Assert metadata inventory/digest/export/forget prefixes stay unchanged after observations because no persisted representation changes. Test hook byte-identical stdout/exit/receipt/core/spool across absent/slow/unsafe/oversize/failure paths, explicit flags only, unsupported propagation unknown, one payload read, no backlog re-delivery and preserved drain reserve. Disable only selected optional delivery on rollback; legacy capture/history/spool/native sessions remain intact. Fresh independent implementation review, exact-head CI/security and integrated wave are still mandatory.
