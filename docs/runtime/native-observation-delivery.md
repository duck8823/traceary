# Native observation delivery design checkpoint

[日本語](native-observation-delivery.ja.md)

Status: **Proposed, not accepted or implemented**. This is the before-code design input for [the observation-delivery leaf](https://github.com/duck8823/traceary/issues/2466), not a new CLI command reference or native-host certification. The [accepted headless R0](../adr/2026-11-03-headless-runtime-r0.md) remains authoritative. Independent High/security review and main-session adoption must resolve the open contract decisions before production implementation.

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

## Open contract decisions

1. **Public intake name and schema.** Select a closed versioned metadata-only schema before advertising flags or implementing a public command. No proposed example here is an executable CLI guarantee.
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
