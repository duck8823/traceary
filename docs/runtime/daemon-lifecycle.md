# Explicit daemon shutdown and interruption history

[日本語](./daemon-lifecycle.ja.md)

This document describes the shutdown SDK/CLI implemented in the lifecycle workstream. The workstream remains draft: native power/gap intake, update handoff/autostart descriptors and final qualification are not yet complete. Do not treat these commands as release or real-host recovery certification.

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

## Offline history and bounds

```sh
traceary daemon shutdown history --runtime-dir /absolute/private/runtime
```

History opens only existing private metadata. It does not start a daemon, create/migrate a core store, adopt a PID, verify native-session exclusivity or authorize resume. `clean` describes daemon marker publication only; old unknown/orphaned evidence remains unknown even after a later daemon exits cleanly. Old launch/control outcomes are preserved rather than rewritten as exited/delivered.

`lifecycle.json` is owner-private, versioned, bounded to 16 MiB and included in the shared 128 MiB envelope. Shutdown intents are retained across epochs, capped at **1,024**; capacity/storage/schema/ownership errors refuse new mutations without implicit pruning or repair. Existing read-only evidence is not converted into execution authority. Opt-in preference and deferred-update metadata in history are not proof that a host manager/updater is installed or active.

## Crash/restart endpoint ownership

`endpoints.json` is a private, versioned, at-most-4-KiB receipt for exactly the two fixed sockets. Both endpoints are checked as a pair before mutation, rebound and durably recorded before lifecycle epoch publication/readiness. Reclaim requires the actual exclusive daemon lease, the same physical private root, an exact prior-epoch owner/device/inode/ctime match, a bounded Unix dial returning only `ECONNREFUSED`, and immediate root/lease/stat revalidation. A successful connection is active/busy, never cleanup permission. Wrong/missing/corrupt receipts, foreign files, symlinks, unsafe modes, changed stamps and other probe errors refuse without unlinking the sockets or rewriting lifecycle history. A crash before receipt publication may leave an unrecorded socket that intentionally blocks automatic restart; no permissive legacy cleanup is offered.

This recovers only proven owned socket paths. It does not kill/adopt old child processes or authorize session resume. The receipt shares the private 128-MiB metadata budget, and publication failure exposes no ready daemon. No isolation from malicious same-UID races is claimed.

## Remaining qualification

Actual CLI shutdown/cancel/history and isolated finite-agent crash/restart tests are part of implementation validation, not real-AI/native-session or physical macOS sleep/wake certification. A previously ready daemon can restart from its exact private endpoint receipt; unrecognized/unrecorded sockets remain untouched. Power/gap invalidation, update postponement/reminders, truthful opt-in host descriptors and final independent/native/full-CI/wave gates remain mandatory. No host login settings, launchd jobs, workstation sleep or distribution is changed by these commands.
