# Runtime observation projection

[日本語](observation-projection.ja.md)

Status: pure reducer and the existing Runtime/selected-queue integration are implemented. This is not native host, sleep/wake, UI or recovery certification. Native-current ingestion is **not exposed by the production runtime**.

## Ownership and evidence

`domain/runtime.ObservationProjection` owns one full epoch/run/generation/worktree/profile selection. Process, interaction, original capture health, transport connection and historical metadata are separate axes. The existing application Runtime owns the projection alongside its actual child; no new registry, worker, queue, core Session event or persisted schema is introduced.

The existing selected UDS schema/ACK is unchanged. `AdmitObservation` checks live published ownership and creates an opaque actual-owner receipt **before queueing**. `ConsumeObservation` preserves that original local receipt through physical registry revalidation and same-owner recheck. The receipt is not native provenance, age proof across suspend, permission or a control handle; it cannot be decoded from client fields. Direct `RecordObservation` uses the same admission/consumption path.

Derived hook metadata enters only `ObserveUnbound`: bounded last-arrival metadata, with source/version, original boundary, claimed native session/turn IDs and original local receipt. It cannot infer working/waiting from prompt/tool/stop/session boundaries, establish native causality or emit Attention. Unbound volume never consumes the native causal ledger or stops the existing live `LastObservation` sample. That sample clears on exit/uncertainty; historical projection metadata remains separately labeled.

`NativeProjectionEvidence` is a trusted adapter-only semantic port, **not a wire DTO or authorization token**. Syntax validation and claimed IDs do not verify it. Before native current can be enabled, a version-qualified host adapter must independently prove native source, full binding, event identity/order, original intake and freshness; guard both commit and current reads using the **same original #2464 ObservationStamp**, and propagate native gate invalidation to projection invalidation. It must not retrofit a new stamp to old queued/unbound metadata. These prerequisites and real host event-rate/limit qualification remain required in host leaves; synthetic tests prove none of them.

## Causality, expiry and history

- Native sequence is verified host ordering, never receipt sequence or host wall-clock order. New evidence must match the anchored source/native conversation and local continuity revision. Unknown predecessor, same-order conflict or inconsistent identity makes current knowledge unknown.
- Retain the highest verified native frontier even when a predecessor is missing: initial B before A stays unknown, delayed older A cannot restore current, and only a **new fresh** correctly chained C may recover. Stored B is never replayed/promoted after A.
- Native duplicate IDs retain their first accepted monotonic receipt. Duplicates before or after expiry cannot renew/resurrect interaction or emit Attention. The native-only lifetime ledger holds at most 256 hashed identities/content fingerprints; it never evicts old IDs and reclassifies replay as new. Exhaustion makes native current unknown and returns a native-port limit error, **not capture/selected-transport degradation**. Ordinary unbound metadata continues. Native rate/limit semantics must be qualified before enabling current.
- Both working and waiting expire at **60 seconds** from original first nonduplicate accepted intake, not dequeue/commit time. Host wall time is optional display metadata only. Pure expiry is evaluated on reads/mutations; monotonic time alone cannot prove freshness across OS suspend.
- Explicit sleep/wake/gap/restart/clock invalidation advances continuity revision, preserving dedup and history. Causal conflict only clears current knowledge, without advancing continuity revision; new independently verified correctly chained evidence may recover. Native conversation cannot be reset in place; verified same-conversation recovery/new actual ownership belongs to #2478.
- Stop never means process exit/readiness/safe paste. Approval telemetry never means visible human wait, permission or approval. Actual process state/publication uncertainty comes from the original owner, not telemetry. Application uncertainty invalidates projection and rejects later admission even if the process enum is live.
- Last waiting retains its original receipt/display metadata and a stale flag, independently of current interaction. Attention identifies one accepted transition into waiting, not repeated waiting events, duplicate arrival, OS notification or authority. Its revision is the accepting snapshot version. Rejected trusted evidence may remain in bounded dedup/last-arrival history; history is never proof of current.

## Snapshot subscription

`SubscribeObservations` returns the actual immutable RunSnapshot, independent ProjectionSnapshot and the existing shared change channel atomically under the owner lock. No lost-wake window, per-client queue or new worker is created. Observation notification does **not increment launch-list revision**, so telemetry cannot invalidate launch-page continuation.

The process axis is refreshed from the actual LaunchDescriptor on each subscription read. Owner exit/uncertainty invalidation happens independently before that read; telemetry never supplies process truth.

This is latest-snapshot subscription, **not an exactly-once event/Attention stream**. Compare full selection and projection Version; skipped versions mean intermediate snapshots may be missing. Unrelated owner/control activity can also wake readers. There is **no expiry-channel wake promise**: a reader must schedule a reread no later than positive `RefreshAfter`, as well as listening to Changed. Native-current reads additionally require the original continuity gate described above.

## Validation and rollback

Fake epoch-monotonic tests cover both TTLs, delayed original intake, duplicates before/after expiry, native ordering/permutations, initial missing predecessors, conflicts, foreign binding/source/session, future receipts, host-time nonauthority, restart/wake/gap, process/health separation, last waiting and Attention. Runtime tests cover opaque owner receipt, original timestamp, atomic subscription wake, interleaved launch pagination, over-256 unbound arrivals, and actual-exit journal uncertainty. A real selected UDS/finite owned PTY fixture checks the production unbound path and unchanged metadata digest; it is not native host qualification.

Rollback disables projection/Attention and returns unknown plus existing historical metadata. It must not delete history, change core Session semantics, replay input, reset/adopt saved PIDs, rewrite host configuration or implicitly resume agents. Physical sleep/wake verification remains explicitly deferred and **NOT_RUN**, not passed.
