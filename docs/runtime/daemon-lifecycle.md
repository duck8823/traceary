# Explicit daemon shutdown and interruption history

[日本語](./daemon-lifecycle.ja.md)

This document describes the implemented shutdown SDK/CLI, update handoff, default-off opt-in descriptors and continuity fencing. On2026-11-05 the maintainer explicitly separated safe implementation integration from physical/native certification. [Physical lifecycle qualification](https://github.com/duck8823/traceary/issues/2494) and [final installed-AI qualification](https://github.com/duck8823/traceary/issues/2498) remain incomplete. Implementation integration is not release or general real-AI recovery certification.

## Detach is not shutdown

Closing an output reader/window only detaches it. It does not close the owned PTY or terminate an agent. Full shutdown is a separate preview/confirm operation. Closing a PTY master can cause **SIGHUP, loss of the controlling terminal, agent exit and work loss**. A master close does not prove child/descendant death. The daemon retains its lease and management endpoint until actual owned waits and admitted coordinators reconcile. A HUP-ignoring child may keep it draining indefinitely; only a separate explicit original-group `runtime stop` is available. There is no automatic TERM/KILL escalation, orphan sweep, adoption, input replay or session resumption.

The daemon itself owns the masters. A separate PTY holder is not required by the permanent safe explicit same-native-session recovery specification. Restoring the old PTY/process is not promised.

## Preview the entire exact roster

Initialize/register the private runtime using [managed launch](./managed-launch.md). Replace every `<...>` below with the exact returned value; `/absolute/private/runtime` is a placeholder, not an installation target.

```sh
traceary daemon status --runtime-dir /absolute/private/runtime --json
traceary daemon shutdown preview --runtime-dir /absolute/private/runtime --epoch <epoch>
```

Preview has no native effects and does not freeze new launches. The CLI fetches every bounded page and verifies the complete roster count/digest before displaying JSON. Rows include selection/state/publication uncertainty only, not terminal bytes, commands, environment or saved PIDs. Actual active ownership is capped at 256; historical uncertain rows can outlive active processes, so the preview is bounded by the 1,024 launch-record limit. Those rows do not create extra native-control rights. Pending launch/control/exit-publication counts can prevent confirmation.

Keep **all eight** original binding values. A digest/ID is correlation, not a cryptographic assertion that a human read or approved anything. New launches, control admission/completion, exits or publication changes make the old plan stale. Fetching the next page does not replace the plan; creating another preview does.

## Cancel, confirm and inspect

Use the same binding flags for `cancel`, `confirm` and `receipt`:

```sh
traceary daemon shutdown cancel --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --preview-id <preview-id> --revision <revision> \
  --roster-digest <digest> --total-runs <total-runs> \
  --pending-launches <pending-launches> --pending-controls <pending-controls> \
  --pending-reconciliations <pending-reconciliations>
```

Cancellation discards only the original preview before confirm admission. It never restores closed terminals. To confirm, replace `cancel` with `confirm` and additionally supply **`--acknowledge-work-loss`**. Every counter must be canonical decimal (`0`, not `00`); no arbitrary PID, filename, executable or signal is accepted. The server rechecks the current exact epoch/revision/roster and zero pending work before the durable intent and native effects. New launch/input/resize admissions are closed after commitment; separately explicit guarded original-group Stop remains available for a live child.

`draining` means terminal closure, **not** daemon/process/descendant exit. `pending`/`unknown` never means delivered or safe to repeat. Caller cancellation, lost/malformed/contradictory ACKs, partial closure or ambiguous persistence report `unknown`. A final Stop ACK can be lost during completed daemon teardown. Neither client nor daemon automatically resubmits an uncertain effect. Use `receipt` with the original binding while the owner is reachable; after exit use read-only history.

An ambiguous intent reservation freezes new launch/input/resize without closing masters or automatically treating shutdown as committed. The frozen owner retains its original read-only decision receipt and separately explicit guarded Stop. Even an empty roster cannot publish a clean marker after an uncertain lifecycle commit; later actual child exit does not erase that uncertainty. A general launch-admission freeze alone does not revoke explicitly acknowledged safe shutdown.

## Offline history and bounds

```sh
traceary daemon shutdown history --runtime-dir /absolute/private/runtime
```

History opens only existing private metadata. It does not start a daemon, create/migrate a core store, adopt a PID, verify native-session exclusivity or authorize resume. `clean` describes daemon marker publication only; old unknown/orphaned evidence remains unknown even after a later daemon exits cleanly. Old launch/control outcomes are preserved rather than rewritten as exited/delivered.

`lifecycle.json` is owner-private, versioned, bounded to 16 MiB and included in the shared 128 MiB envelope. Shutdown intents are retained across epochs, capped at **1,024**; capacity/storage/schema/ownership errors refuse new mutations without implicit pruning or repair. Existing read-only evidence is not converted into execution authority. Opt-in preference and deferred-update metadata in history are not proof that a host manager/updater is installed or active.

## Crash/restart endpoint ownership

`endpoints.json` is a private, versioned, at-most-4-KiB receipt for exactly the two fixed sockets. Both endpoints are checked as a pair before mutation, rebound and durably recorded before lifecycle epoch publication/readiness. Reclaim requires the actual exclusive daemon lease, the same physical private root, an exact prior-epoch owner/device/inode/ctime match, a bounded Unix dial returning only `ECONNREFUSED`, and immediate root/lease/stat revalidation. A successful connection is active/busy, never cleanup permission. Wrong/missing/corrupt receipts, foreign files, symlinks, unsafe modes, changed stamps and other probe errors refuse without unlinking the sockets or rewriting lifecycle history. A crash before receipt publication may leave an unrecorded socket that intentionally blocks automatic restart; no permissive legacy cleanup is offered.

This recovers only proven owned socket paths. It does not kill/adopt old child processes or authorize session resume. The receipt shares the private 128-MiB metadata budget, and publication failure exposes no ready daemon. No isolation from malicious same-UID races is claimed.

## Power changes and causal observation continuity

On macOS the daemon reads only the fixed `kern.sleeptime`/`kern.waketime` kernel timestamps. It requires two identical validated pairs and checks the completed read's duration/clock bounds. A changed pair, clock reversal, unavailable/torn read or watcher gap of at least 30 seconds revokes old intake contexts. A readable sleep still in progress remains unknown. The one-second watcher invalidates idle/in-flight evidence, but intake and guarded projection reads/commits also refresh directly, so a short sleep between polls cannot make an old stamp current. None of these reads requests sleep or changes host power settings.

An explicitly composed causal receiver gets an opaque stamp **before enqueue**, never at delayed dequeue. Queue overflow and consumer errors revoke queued/in-flight scopes; stale items cannot acquire fresh stamps. A stalled native read is single-flight per gate, with a complete caller wait bounded to 250 ms, never holding the process-ownership lock. Late reads cannot reopen a revoked scope, and daemon shutdown does not join an uninterruptible native read. The gate's revision and per-run native-event revisions are independent. A new healthy poll alone does **not** restore native interaction knowledge: a new independently validated event for the complete current binding is still required.

This is an adapter contract, not host certification. A causal receiver must validate descriptor/host/version/worktree/run/generation/epoch/native-event causality and guard both projection commit and current read with the original stamp. Nonempty metadata and transport acceptance are not that validation. When a causal receiver is explicitly configured, it is the sole delivery path: unsupported power evidence never falls back to a history receiver. Other platforms currently have no certified power witness; legacy history-only delivery remains available without claiming native freshness. Normal one-shot SDK socket closure is not a communication gap. No new input/approval/stop/resume authority is created, and no host-specific native receiver is implicitly enabled by these commands.

## Update policy implementation boundary

The owner API now records continuous update deferral without stopping children or freezing ordinary launches/controls. Status includes the full pending/run/control/reconciliation/lifecycle/uncertainty blockers, not just a process count. Retargeting and epoch initialization preserve the original UTC start and prior notice publication. Status reads are non-consuming; every supported explicit attempt returns an attempt notice, including busy/persistence failures. A successful idle attempt returns only an exact empty shutdown preview, not permission to race an installer.

A separate foreground scheduler durably publishes the private metadata notice after 24 hours; it does not delay native power polling. `notified_24h` is publication deduplication, **not proof of human display or OS notification delivery**. Overdue status remains readable even after a lost response. Lifecycle metadata coordinators participate in actual owner joins and invalidate earlier shutdown previews; ambiguous commits freeze new effects and forbid a clean marker. No binary updater is executed. Local opt-in descriptors are described below; physical/native qualification remains pending in the separate follow-ups.

The owner-side update handoff reuses the shutdown coordinator but structurally requires an exact **empty** digest/roster, zero pending counters, the matching target and explicit daemon-exit acknowledgment. New work makes its preview stale; it cannot close a live PTY. The existing private envelope atomically finalizes the prior shutdown intent and retires the matching deferral before the coordinator joins. An error preserves uncertainty instead of claiming installed software. Same-ticket joins must match both original purpose and target. A successful `draining` receipt with zero closed terminals still does not prove OS exit, update installation or old-session exclusivity. Normal confirmed shutdown waits a bounded interval for already-registered final response writes, not for accept/attach loops; transport loss still means unknown, never permission to replay.

## Explicit update CLI

```sh
traceary daemon update status --runtime-dir /absolute/private/runtime --epoch <epoch>
traceary daemon update attempt --runtime-dir /absolute/private/runtime --epoch <epoch> \
  --target-version <version> --target-protocol <canonical-number>
```

Status is non-consuming; an attempt prints a notice and current-owner metadata, not installation success. Active/pending/uncertain work returns `deferred`; a quiescent attempt returns `quiescent_snapshot_not_permission` plus an exact empty `preview`. A supported refusal can still print its notice and return an error. A possibly persisted/lost/malformed/mixed/queued/mismatched attempt response is `unknown`, with no automatic retry. Inspect status rather than replaying it. A healthy poll or notice does not authorize native input, approval or session resume.

For explicit empty handoff, use `daemon update handoff`, both target flags, **all eight** original preview flags listed in the shutdown section, and **`--acknowledge-daemon-exit`**. Zero/pending counts and the digest must remain exact. `--acknowledge-work-loss` is not an update-handoff substitute; a nonempty preview is always refused. The CLI prints `current_owner_metadata_only_not_installation` and never starts an updater, creates a core store, starts a daemon/agent, installs launchd, or retries an uncertain effect. After handoff, inspect the original shutdown receipt while reachable or existing-root history after exit. No requested version/protocol is claimed installed.

## Remaining qualification

Actual CLI shutdown/cancel/history, isolated finite-agent crash/restart, read-only native power API access and synthetic causal queue/wake tests are implementation validation, not real-AI/native-session or physical macOS sleep/wake certification. A previously ready daemon can restart from its exact private endpoint receipt; unrecognized/unrecorded sockets remain untouched. Authorized physical macOS sleep/wake, host-specific binding/reduction, update postponement/reminders, truthful opt-in host descriptors and final independent/native/full-CI/wave gates remain mandatory for certification. Implementation integration still requires independent review, fresh CI/security and its scoped implementation wave. No host login settings, launchd jobs, workstation sleep or distribution is changed by these commands.

## Local login auto-start preference (no installation)

Auto-start is off by default. These commands do not start a daemon or agents, open the core database, inspect Login Items, install/remove a job, or invoke `launchctl`.

```sh
traceary daemon autostart status --runtime-dir /absolute/private/runtime
traceary daemon autostart opt-in --runtime-dir /absolute/private/runtime --enabled=true
traceary daemon autostart descriptor --runtime-dir /absolute/private/runtime
traceary daemon autostart opt-in --runtime-dir /absolute/private/runtime --enabled=false
```

`opt-in` requires the explicit `--enabled` flag, including for false. It may create only the selected owner-private runtime metadata root. `status` and `descriptor` require an existing root and never create a missing one. Status reports `opt_in`, `installation_state: "unknown"`, `host_inspection: "not_performed"`, and `starts_agents: false`. Opting out changes local preference only; it does not remove an independently installed host job.

On macOS, `descriptor` additionally requires prior opt-in and prints an escaped plist to stdout. Its root must be canonical, printable UTF-8, owner-private, physically unchanged, and short enough for the runtime sockets. The executable is the actual current executable, canonicalized and checked for trusted ownership, safe permissions, stable identity and content. The descriptor has a root-derived stable label, exactly `[currentExecutable, daemon, serve, --runtime-dir, canonicalRoot]`, and `RunAtLoad=true`; it contains no environment, shell, agent, resume or update command. Other platforms explicitly reject descriptor rendering. Rendering is not installation or permission to install. Executable/root witnesses are rechecked before returning, not a guarantee against later filesystem changes. Host installation, settings and distribution require separate authorization and are not performed by these commands.

## Integration with observation and native recovery

The lifecycle branch now integrates the current main observation/recovery implementation rather than replacing it. Lifecycle enrollment is refused after any launch/resume ticket, including a completed or definitively refused receipt; it cannot publish a new startup marker after execution admission. Normal launch and explicit native resume share the lifecycle admission fence; a preparing/committed shutdown or lifecycle startup blocks a new execution, but an existing completed receipt remains inspectable without replay. An attached resume remains pending publication in the exact shutdown/update counters until its durable result completes. An ambiguous resume with no transferred handle retains its existing pending reservation and cannot produce a clean marker; busy/unknown is not exit. Lifecycle/recovery response families reject one another, including known-refusal error replies.

The single observation drainer retains current main's owner-private unbound intake receipt and the optional causal receiver's original before-queue continuity stamp in their respective paths. Neither local receipt nor a healthy sample grants native-current authority. No new ObserveBound path is enabled; any future qualified native-current commit/read still requires the same original stamp. PTY lifecycle closure retains the continuous reader and private Claude recovery metadata, whose snapshot/cleanup still follows actual Wait. Physical sleep/wake remains user-deferred NOT_RUN; synthetic integration does not certify it or complete native qualification.
