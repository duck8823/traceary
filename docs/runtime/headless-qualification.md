# Integrated headless daemon qualification

[日本語](headless-qualification.ja.md)

**Implementation checkpoint; native certification incomplete.** [Implementation QA](https://github.com/duck8823/traceary/issues/2470) is separate from [installed-AI qualification](https://github.com/duck8823/traceary/issues/2498), not release or native certification. UI/framework decisions are separate. The physical sleep/wake test remains explicitly user-deferred, not passed. Muse native work remains deferred.

## Integrated synthetic process fixture

`TestRuntimeCLIEndToEndRegistrationLaunchAndListWithoutHomeOrCoreBootstrap` reuses actual public CLI subprocesses, a real isolated daemon and its directly owned synthetic PTY executable. No HOME, credential or installed model is used. An opaque existing core-store file must remain byte-identical and the poison core path must not be initialized.

The `integrated` scenario establishes observation admission as **unbound**, reads live output, detaches only the read-only attach client and waits for that client. It verifies the same managed generation is still live, rejects a foreign-generation attach, then reconnects at the exact byte cursor **before any manual input**. It sends explicit bytes, inspects the duplicate-request receipt, resizes, verifies a marker and geometry in the child, explicitly acknowledges work loss and stops. The daemon must reconcile actual child exit; the reconnected stream must end without replaying consumed data. A stop-delivery receipt alone is not exit evidence.

The fixed positive workload is one child, exactly2MiB of `x` output in64KiB bursts acknowledged by a fixture-private consumed-cursor file after actual decoding, five raw input bytes, one `r` marker, one duplicate same-request receipt inspection and a31x101 resize. The existing30-second fixture context is a failure bound, not an AI latency or performance SLA. Selection and contiguous canonical decimal cursors are checked on every live frame. Raw output is never interpreted as state or approval.

The separate `replay-oracle` case intentionally sends the same five bytes with a **new** explicit synthetic request ID. The child requires literal `r` as its next byte; unexpected replay begins with0xe6 and exits97 before recording geometry. This confirms the positive duplicate-request test would fail if input were replayed. Distinct manual requests are not required to deduplicate. The original output/control/observe cases retain their unpaced2MiB burst as regression coverage. An earlier unpaced integrated race run reported a real slow-consumer gap; its failure is retained. The private-file handshake determines only the positive synthetic workload, not native backpressure or resource performance, and never accepts a gap as success.

This is real process/CLI integration using a synthetic executable. It does **not** prove installed-AI hooks, bound native observation, native currentness, final model/provider, interrupted-session recovery, sleep/wake or system resource thresholds.

## Remaining native/full qualification gates (separately tracked)

- Installed native launch/output/manual input/detach/reconnect, qualified bound observation and expiry, actual stop; explicit unsupported/unknown hosts.
- Foreign/stale run, PID reuse, daemon absence/crash, partial input, slow consumer, overflow/cursor gap, maintenance, shutdown cancellation and update deferral fault matrix.
- Physical sleep/wake: user-deferred NOT_RUN; do not infer success from timers.
- Native workload/device/process-tree and bounded metadata/output evidence. CPU/RSS measurement is explicitly deferred, not PASS; preregister the workload/machine/limits when that study resumes.
- Existing CLI/hooks/redaction/spool/history/memory compatibility without a daemon.
- Exact native interruption/resume at the original target, new generation and no pending-input/tool/approval replay; old-owner live/unknown, concurrent resume, changed target, missing history and recovery crash refusal.
- Required independent High review, final-head build/full CI/lint/security and approved wave E2E on the same integrated SHA.

A green synthetic test never substitutes for these gates. No runtime/auth/default/config/schema change, wrapper, automatic adoption/kill/resume, UI adoption or distribution is authorized by this fixture. Rollback removes only its new test scenarios/helper and this qualification note. Parent issues remain open through the actual tagged release.

## Mismatched-selection input refusals

Every control-bearing scenario additionally submits an explicit five-byte synthetic payload using one syntactically valid but mismatched epoch, run, generation, worktree or profile at a time. Each public CLI call must fail with a safe rejected/zero-written receipt and must not echo the payload. Looking up its request ID through the correct selection must remain not-found; the original child must have no input artifact. The following exact positive input and literal-marker oracle also detect delayed unintended writes. No invalid request is retried. These unknown/mismatched selectors are not a substitute for the remaining concurrent-existing-run/native ownership matrix. Runtime behavior and limits are unchanged.

## Lifecycle-dependent integrated source

The original lifecycle slice98536b18c53130fd80604b04cc99c50fe9dd2747 is combined with reviewed Codex/AGY implementation and the unsupported-platform guard. QA adds no runtime authority; the host-dispatch and post-actual-Wait cleanup union was separately reviewed and tested. One shared process fixture preserves all nine scenarios: output, controls, observe, integrated, replay-oracle, shutdown, crash, update and autostart. In the integrated live run, after reconnect and before manual input, one update attempt must defer with a notice; one exact single-run/zero-pending shutdown preview is canceled without confirmation. The same child/generation must remain live for the subsequent exact input/no-replay/resize/Stop checks. This is metadata deferral/preview cancellation, not update installation, irreversible shutdown rollback or native-state authority.

The QA PR originally targeted the lifecycle feature branch for review isolation. Lifecycle implementation has since merged and QA was explicitly retargeted to main. **Never merge QA into the feature parent.** On2026-11-05 the maintainer approved separate implementation integration and unfinished physical/native certification. Implementation closure still requires source identity, independent High/security review, fresh full CI/lint and an implementation-only wave at the fixed integrated input. Native/physical requirements remain unfinished in the follow-ups; failed/unknown/NOT_RUN never becomes PASS, no unqualified authority is enabled, and the original whole-daemon goal remains incomplete.

## Concurrent same-target synthetic children

The additional `concurrent` scenario launches two simultaneously live synthetic children using the same canonical worktree, core-store target and profile. Run and generation identities must differ. Crossing the first real run with the second real generation must reject input with zero bytes, no input artifact and typed not-found receipts in both actual journals.

Each child then receives its own distinct five-byte payload under the **same** request ID. Exact duplicates inspect their own receipt without replay; literal-marker and distinct resize geometries detect crossed or repeated input. Per-run artifacts must confirm the original canonical cwd/store and run identity. Acknowledged Stop of the first child must reconcile actual exit while the second remains live; only its separate explicit Stop may end the second. Both exits must be signal exits, not the finite fixture's expiry. The daemon's actual Wait follows both reconciled exits; core bytes and no-bootstrap constraints remain unchanged.

Together with the retained-cursor scenario below, the fixture now has eleven modes. This does not qualify concurrent installed AIs, reused native conversation IDs, native generation binding, native interruption/resume or resource thresholds. Existing final gates and parent-first delivery order remain required.

## Refusal after actual daemon exit

After the integrated child and its original daemon have actually exited, the fixture submits valid retired-selection attach/input/resize/Stop requests once each. They must fail without echoing the synthetic input, changing bounded runtime metadata, restarting the original child or initializing the core store. The owner socket must already be absent after actual daemon Wait; unchanged file modes/content hashes also detect implicit owner creation.

Optional observation keeps its existing best-effort CLI contract: exit0 with exactly `unavailable`, `unbound` and `admission_only`, not a queued/current-state claim. It must likewise leave runtime/core/native artifacts unchanged. The initial test incorrectly required observation to return nonzero; that oracle failure is retained and corrected against the existing documented implementation, not presented as a product fix. No native/global exclusion or failure-window qualification is inferred.

## Exact retained-history eviction through the public CLI

Only `cursor-gap` emits a fixed2MiB+32KiB of `x`; all previous modes retain their original workloads. The default2MiB retention and32KiB chunk bounds are checked before launch, not silently adjusted. After actual child exit7, attaching at cursor0 must report ended capture, base32768/head2129920, then exactly one known `evicted` gap[0,32768) with delivery_unknown=false. Every retained byte must follow contiguous canonical cursors through the exact head, with unchanged selection on every frame, an end marker, exact helper trailer and actual read-only-client Wait0.

Attaching at32768 must replay the retained2MiB without a gap; reconnecting at2129920 must replay no data. Original core/target/no-bootstrap and actual daemon-Wait checks remain. The initial inherited2MiB producer failed the new eviction oracle; increasing only the new synthetic workload supplied the missing pressure. That RED is retained as a fixture deficiency, not a runtime defect. History eviction is not a slow-live-consumer, unknown-tail, native-AI or resource-performance qualification.

## Maintainer decision: CPU/RSS qualification deferred (2026-11-05)

The maintainer explicitly deferred CPU/RSS measurement and will assess responsiveness experientially later. CPU/RSS measurement is therefore not a blocking gate for the current headless delivery. This is a deferral, not a measured resource-budget PASS: the Stage A calibration remains synthetic-only and native process-family CPU/RSS coverage remains unproved. No sampling method or numerical budget was newly accepted. Exact byte/ack checks, bounded metadata/output, process ownership and actual exit evidence remain required. Other native, sleep/wake, security, review and wave gates are unchanged.

## Implementation fault evidence layers

| Fault | Evidence, not native certification |
| --- | --- |
| Foreign/stale selectors, concurrent runs, duplicate input receipts | Public CLI with directly owned synthetic children; exact zero-effect/no-replay assertions |
| Absent/crashed daemon, cancel/update deferral, cursor eviction | Real daemon/CLI processes; exact bytes/cursors/receipts and actual Wait; finite synthetic producer |
| Partial input and closed-handle reuse | Injected syscall/write boundary and unrelated real pipe; not OS PID reuse or native input interruption |
| Slow subscriber, output overflow, PID reuse | Application/fake-child models; explicit unknown gaps, independent healthy reader and epoch rejection |
| Queue pressure and causal loss | Actual metadata lock/socket queue plus injected worker revocation; no stale stamp restamping |
| Maintenance and committed history | Actual isolated SQLite transaction/lease; Busy while exclusive, readable after release |
| Physical sleep and native callback/currentness/recovery | NOT_RUN/unqualified or retained measured scope only, never inferred from the rows above |

The implementation-only wave runs synthetic real-process tests and isolated synthetic daemon-free record/search/refine/memory. Host-record SKIPs remain explicit. It does not install plugins, answer trust, start installed AIs or replace native compatibility/wave certification. CPU/RSS remains deferred; no UI, distribution, host settings, auth/model/provider or automatic replay is authorized.
