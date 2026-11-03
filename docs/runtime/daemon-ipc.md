# Owner-scoped local daemon IPC

[日本語](daemon-ipc.ja.md)

This implements the transport slice of [R0](../adr/2026-11-03-headless-runtime-r0.md).
It does not yet launch agents, own PTYs, interpret native waiting states or resume
sessions. Those remain separate runtime implementation/certification leaves.
No wrapper, TCP listener, model request, core-store migration or login/configuration
change is introduced.

## Explicit operator commands

```sh
traceary daemon serve --runtime-dir /absolute/private/runtime
# In another terminal:
traceary daemon status --runtime-dir /absolute/private/runtime --json
```

`serve` runs in the foreground; with managed children, interruption enters an ownership-preserving drain (see [managed launch](managed-launch.md)); it is not login auto-start or
an implicit background launcher. Status never starts a daemon. If omitted, the
root is `$XDG_STATE_HOME/traceary/desktop/runtime`, defaulting to
`~/.local/state/traceary/desktop/runtime`. The root must be absolute, safe and short
enough for native Unix socket paths. Symlink/private-root mode failures are refused,
not silently repaired. `--db-path` is not a daemon option: this namespace has no
core-store selection. A daemon-only composition is selected before core-store
path/config bootstrap, so explicit roots work with HOME unset. Core reclaim
inspection is excluded from these commands.

A status object reports `protocol_version`, a fresh 32-hex `epoch`,
`state: listening` (or `draining` when new launch admission is disabled), `capabilities` and `observation_delivery`. **Listening is
transport readiness, not running/working/waiting/approval or input authority.**
The CLI has no native observation receiver yet and returns `unsupported` to observations;
managed `launch`/`list_runs` capabilities are described in [managed launch](managed-launch.md). It never falsely reports
accepted native state. Applications may inject the typed receiver port. `queued`
then means only transport queuing, not persistence, causal binding or interpretation.

## Filesystem, process and peer boundary

The 0700 current-UID root is no-follow-walked and descriptor-pinned using the same
safe directory implementation as the registry. A bounded process-local claim
and a private 0600 `daemon.lock` OS lock exclude cooperating duplicate instances.
Lease identity is revalidated before serving requests. No stale socket is
unlinked automatically: an existing path refuses startup. If the second endpoint
cannot start, the first owned endpoint is cleaned up without touching the conflict.

Two 0600 sockets have immutable roles:

- `daemon.sock`: named management methods (`status` and the separate typed managed-launch/list slice). No generic exec,
  file access, reflection, arbitrary shell or implicit process control.
- `observations.sock`: fixed derived-metadata `observe` only. Never input/stop,
  approval, Memory mutation, management status or role switching.

Client and server both verify native peer UID. macOS uses
`LOCAL_PEERCRED`/`Xucred` with the native version-zero ABI; Linux uses
`SO_PEERCRED` for CI fixtures. Other platforms fail closed with unsupported
transport while the existing CLI still builds. Peer UID does not authenticate a
particular native host. Malicious same-UID software and untested ACL/cross-UID
cases are not certified isolation boundaries.

Listeners disable automatic pathname removal. Cleanup checks the pinned root and
matching owner/type/device/inode before unlinking an owned socket; observed
replacement files/sockets are preserved. Same-UID adversarial path races are not
claimed fully isolated by pathname checks. No user worktree/store/history is removed.

## V1 canonical frames

Each frame is a 4-byte unsigned big-endian byte length followed by one UTF-8 JSON
object, 1–65,536 bytes excluding the prefix. Invalid UTF-8, duplicate/case-aliased
keys, excessive nesting, unknown fields and nullable v1 values are rejected.
Unknown versions are unsupported; no unknown schema is guessed compatible.

Handshake request fields:

```json
{"protocol_version":1,"request_id":"example-hello","operation":"hello","role":"management"}
```

The socket fixes the role; changing `role` does not elevate access. After a valid
hello, requests carry a fresh bounded `request_id` and an explicit operation.
Status uses `operation: status` with no observation. Observation uses
`operation: observe` and one fixed object with `host`, `host_version`, `run_id`,
`generation`, `epoch`, `target_id`, `event_id`, `native_session_id`, `kind`, and
optional `native_turn_id`. Values are bounded printable opaque identifiers, not
raw prompt/code/audit/terminal bodies or command arguments. The receiver must
independently validate host/version/native witness/generation/order; these fields
and the daemon epoch never confer control permissions.

Responses have the version, matching request ID and a fixed code: `ok`, `queued`,
`invalid`, `unsupported` or `busy`; status adds its typed readiness object.
Malformed/truncated/oversize frames may be closed without acknowledgement.
Duplicate request IDs are refused within a connection, bounded to 1,024 IDs.
This is not across-connection exactly-once execution or native-event deduplication.

## Admission, time and failure

- At most 32 management connections and 32 observation connections per daemon;
  excess admission returns busy without creating a handler goroutine.
- At most 256 pending observation frames **and** 8 MiB, whichever fills first.
  One consumer worker; queue/consumer failure latches `observation_delivery: degraded`.
  This diagnostic is not proof of a native interaction state or healed event gap.
- A handshake/frame deadline of at most one second bounds slow peers. Constructor
  limits may tighten, never expand the fixed ceilings.
- `Status`/`Observe` client calls have a total monotonic 250 ms optional budget,
  including path inspection/dial/handshake/write/ack. Potentially blocking inspection
  runs behind 32 process-wide slots retained until actual completion: returning
  a timeout cannot accumulate unbounded background workers or cause a late send.
- A receiver must honor cancellation. An unresponsive injected consumer retains
  a bounded root/lease claim until it exits; the transport does not wait indefinitely
  or admit a second owner into that uncertain instance.

Frame rejection and transport errors do not initialize/query the core DB, alter
host stdout/approval decisions or imply successful capture. Native hook forwarding,
receipt/spool compatibility, state projection, active-run shutdown and safe native
session recovery are downstream responsibilities; this transport does not claim
their validation complete. No raw frames, environment or receiver error payloads
are logged or reflected as diagnostic text.

## Evidence and rollback

Tests cover real native Unix peers in both directions, separate-process CLI serve
and status with HOME unset, schema/role rejection, frame limits, slow/truncated
peers, duplicate IDs, count/byte pressure, lease/path replacement, exclusive startup,
cleanup preservation, client deadline and fuzz seeds. Windows cross-build checks
existing CLI compatibility, not Windows daemon certification. Different-UID and ACL
native certification remains explicitly unrun; do not infer it from same-UID tests.

Rollback stops admission/cancels transport connections and removes only confirmed
owned sockets. Keep registry/core/history/Memory and conflict artifacts intact.
Stopping a transport is not approval to stop agents; active-run lifecycle policy
is implemented by its separate owner before the integrated runtime is qualified.
