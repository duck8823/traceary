# UI-independent local runtime checkpoint (R0)

[日本語](2026-11-03-headless-runtime-r0.ja.md)

- Status: **Accepted by the maintainer** when merging [#2473](https://github.com/duck8823/traceary/pull/2473) on 2026-11-03; implementation/certification gates remain per leaf.
- Design issue: [#2471](https://github.com/duck8823/traceary/issues/2471).
- Parent: [#2414](https://github.com/duck8823/traceary/issues/2414).
- Baseline: `dc6055f6545749c449708f621217a60bc4a105f7`.
- Platform: macOS, local owner only. No public TCP, remote relay or cloud service.
- Decision authority: main-session design discussion and human acceptance. A goal,
  successful fixture, review recommendation or PR creation does not accept R0.

## Requirement and decision boundary

On 2026-11-03 the maintainer explicitly separated daemon work from UI adoption.
A headless Go daemon must own managed agent processes and PTYs across client
close/reopen, expose bounded output and explicit target-bound operations, and
project only evidenced native interaction states. Existing CLI capture continues
without the daemon. Authentication, billing, permissions and approval decisions
remain with the real agent CLI.

The former Desktop D0 blanket stop is replaced **for headless scope only** by R0.
[UI D0 #2424](https://github.com/duck8823/traceary/issues/2424) remains open;
Electron and Wails remain unadopted. IME, VoiceOver, monitors and other UI gates
remain mandatory for their UI/native integration track, not prerequisites for R0.
R0 design/docs/isolated probes may proceed now. Product runtime implementation
starts only after this contract is reviewed and explicitly accepted by the human.
No prior negative measurement, unknown result or hardware gate is reclassified.

Out of scope: wrappers, PATH shims, aliases, shell-profile rewrites, a mandatory
`session run`, automatic agent resume, credential reads/copies, new model APIs,
core store migrations, Session running/ended lifecycle, review auto-send,
automatic Memory acceptance, Desktop implementation, signing and distribution.

## Conceptual model and invariant owners

| Concept | Behavior / owner | Invariant |
|---|---|---|
| Session | Existing core history grouping | Timestamps and IDs are not liveness or authority |
| Target | Registry canonical project/worktree/store identity | Switching selection never rebinds a launched run |
| Host profile | Operator-owned executable/version/argv policy | Management clients supply profile ID, not arbitrary executable/argv/env |
| Run | Runtime-owned child handle, immutable target/profile | Saved PID, cwd and conversation ID alone never authorize input or stop |
| Generation | Fresh launch identity plus daemon epoch | Old callbacks/receipts cannot change a replacement run |
| Binding | Descriptor plus independently verified native witness | Environment hints and possession of a descriptor path are not authorization |
| Observation | Adapter evidence, source identity, monotonic receipt time | Unknown causality or version stays unknown |
| Projection | Pure reducer of accepted observations | Display state cannot create approval/input authority |
| Recovery record | Durable native session identity and target; exclusive resume intent | Same conversation, fresh run; never infer old-owner absence or replay input |
| Input receipt | Request identity and bounded delivery outcome | Partial or interrupted delivery is unknown, never automatically retried |

Process, interaction, capture health, client connection and committed history
remain separate axes. `Stop` is a turn boundary, not process exit or evidence of
safe automated input. A permission event may be telemetry, not visible human wait.

## Responsibilities, packages and interfaces

| Owner | Owns | Does not own |
|---|---|---|
| `domain/runtime` (new) | Run/target/generation identity, projection and bounds invariants | SQL, socket DTOs, OS child handles |
| `application/runtime` (new) | Consumer-oriented registry/runtime services and narrow ports | CLI rendering, protocol parsing, native syscall details |
| `infrastructure/runtime` (new) | Private metadata, Unix IPC, PTY/child ownership, host adapter I/O | Core Session lifecycle, billing/auth decisions |
| Existing `application/usecase`, query services | History/Memory/refinement rules and core persistence | Terminal buffers and registry layout |
| `presentation/cli` | Explicit operator commands, validated protocol conversion | Native host state guesses and unbounded service logic |
| Later native UI | Typed allowlisted consumer, human interactions | Raw SQLite/socket/file/shell or credentials |

These are responsibility boundaries, not a generic plugin framework. File layout
may be refined within each issue without changing this public contract. Compare
existing `MemoryUsecase` in `application/usecase/memory_usecase.go` and its
implementation with `presentation/cli/session_run.go`: use object-level methods
on Registry and Runtime services, not one usecase class per RPC. Interactive PTYs
must not be appended to the existing one-shot runner. Core domain/usecases gain
no dependency on runtime infrastructure; composition stays at the entry point.

## Operator surface and compatibility

The additive command groups are `traceary daemon` (serve/status/shutdown planning)
and `traceary runtime` (registry, profiles, launch/list/attach/input/resize/stop/resume).
Exact subcommand flags and JSON output are fixed with their owning implementation
issue before shipping. `traceary runtime observe` is the narrow hook intake; it
is not a generic command runner or approval endpoint. Existing public hook
stdout/decision, exit, redaction, receipt and spool contracts stay unchanged.
No daemon starts implicitly as a side effect of recording a hook.

An operator explicitly registers a real absolute executable and fixed argv as a
host profile through the local CLI. The daemon snapshots and validates that
profile at launch; an untrusted management caller cannot override executable,
argv, cwd, arbitrary environment or store paths. Target selection uses registered
IDs. Inherited host authentication remains available to the real CLI, but the
daemon does not inspect or serialize secrets. Persist only safe launch metadata
and allowlisted non-sensitive override names, never environment values or raw
terminal text. Do not replace HOME or global configuration to simulate isolation.

## Version 1 local IPC contract

Two separate owner-private Unix sockets under
`$XDG_STATE_HOME/traceary/desktop/runtime/` (default
`~/.local/state/traceary/desktop/runtime/`) are proposed:

- `daemon.sock`: typed management requests and output subscriptions.
- `observations.sock`: derived metadata only; no input/stop/approval/Memory operations.

The root must be absolute and canonical, with no symlink component in the private
runtime path, correct UID and non-group/world-writable trusted ancestors. Check
OS socket path length before bind/connect, directory mode 0700 and socket mode
0600. Reject unsafe existing paths, wrong owners/permissions and conflicting live
instances. Never unlink another process's socket. Revalidate owner/device/inode
on cleanup; do not claim full same-UID hostile-race protection from path checks.
Use descriptor-relative/no-follow operations where supported and reject any
unverifiable identity. Client and server both check peer UID; macOS uses the real
OS peer API. Do not replace failed checks with process-name or cwd matching.

Threat boundary: this is a same-owner local service, **not** isolation from
malicious software already executing as that owner. OS peer identity is required
but not proof of a particular native host. A public run ID, session ID or socket
path is not an independent authorization credential. Native observation binding
requires a trusted host/version witness; ambiguity is unbound/unknown.

Frames are a 4-byte unsigned big-endian byte length followed by one UTF-8 JSON
object, at most 65,536 bytes excluding the prefix. Reject an entire oversize,
truncated, invalid UTF-8, unknown-field or invalid-schema frame, not its prefix.
The handshake declares protocol version 1 and role; the selected socket fixes
role and operation allowlist. Reject unsupported versions before any mutation.
Requests carry a bounded request ID; management targets include epoch, run,
generation and immutable target identity. Responses distinguish accepted,
rejected, unsupported, busy, gap, delivery-unknown and exited; English diagnostic
messages never include raw payload, environment, command arguments or secrets.
Binary PTY bytes are explicit base64 fields, not JSON text interpreted as state.

A consumer cannot negotiate the observation role into management. Management
supports only named typed methods; never `exec`, arbitrary file read or generic
method reflection. Native UI sender/frame validation is an additional downstream
boundary and is not replaced by peer UID checks here.

## Bounds and admission decisions

These are conservative v1 admission choices, not performance claims derived from
previous probes. Change them only with an explicit contract/test update. The
implementation must prove memory use and overload behavior under the fixed load.

| Resource | v1 bound / response |
|---|---|
| Observation frame | 64 KiB; reject whole frame |
| Optional CLI delivery | 250 ms total monotonic budget including connect/handshake/write; best effort |
| Observation queue | 256 frames AND 8 MiB per daemon, whichever is reached first; reject busy |
| Management connections | 32 concurrent; excess rejected busy before allocation |
| Per-run PTY output retention | 2 MiB byte ring; evictions advance cursor and report gap |
| Subscriber pending output | 256 KiB per subscription, at most 4 subscriptions/run; disconnect slow consumer with gap |
| Active runs | 256; reject launch without evicting runs |
| Input request | 16 KiB decoded bytes; larger input rejected before write |
| Receipt ledger | 1,024 entries/run, bounded metadata only; no content; reserve 16 for stop/lifecycle, reject new input before exhaustion; never forget uncertain receipts |
| Registry | 100 projects, 1,000 worktrees; overflow rejects mutations |
| Persistent runtime metadata | 128 MiB total; atomic owner-private writes, versioned schema; reject overflow |
| Interaction projection | 60 seconds from first accepted nonduplicate observation, including waits |

Output pumps continue draining the PTY into the bounded ring when UI consumers
are slow. They never wait indefinitely for a subscriber. Gaps and discarded byte
ranges are explicit; exact delivery cannot be claimed across eviction. A metadata
budget is not a PTY buffer budget. Raw terminal data stays memory-only by default.
No silent deletion of registry records, receipts, runs or user drafts is permitted.

## Launch, targeting and explicit control

`Launch(profileID, targetID, requestID)` validates the registered canonical Git
worktree/common-dir and store identity immediately before spawning the configured
real binary directly, without an intermediate shell. Changes or disappearance
fail closed. Bind the owned child handle and immutable descriptor to fresh
run/generation/epoch. Spawn is idempotent within its bounded request ledger;
concurrent duplicate requests cannot create two processes. Launch failure/cancel
must reconcile child ownership before reporting success. Descriptor files are
owner-private and contain no secrets or model prompt.

`Input`, `Resize` and `Stop` require the current target/generation and explicit
human action. Recheck target and child ownership under the operation boundary,
not by loading a saved PID. Manual terminal keystrokes are possible in unknown
interaction state because they are the human's intentional interaction; automated
review paste/Enter and approval decisions are not authorized by that fact.
Input has a request receipt with written byte count and result. Any possible
partial write/crash is delivery-unknown. Duplicate or uncertain receipts are not
replayed. After daemon restart the old epoch is stale, not recoverable exactly-once.
Stop is an explicit process-group operation only on the owned group; descendants
and work-loss are disclosed. There is no automatic orphan signal or PID-based adoption.

External launches remain history/unbound observations, not managed runs. Native
session/resume IDs are correlation only. Supported native host evidence may bind
an observation but never adds input/approval rights.

## Detach, shutdown, crash, update and wake

Client close/crash only detaches; daemon child handles and PTY masters remain.
Reconnect requests a run/generation snapshot and bounded byte cursor; unknown or
evicted cursors receive a gap. Subscription is not a new launch. Process exit is
observed with the owned wait handle, separate from native hook interpretation.

Decision: **the daemon itself owns PTY masters**; no separate PTY holder is required by the recovery specification.
A holder adds another protocol/process ownership boundary and is deferred rather
than pretending crash recovery is free. Daemon crash may close the master, cause
SIGHUP/agent termination and lose work. On restart, recorded active runs are
unknown/orphaned; do not reopen saved PIDs, attach, kill or resume automatically.
Restoring the original PTY/process is not the recovery requirement. The durable
product requirement is **safe, explicit resumption of the same native agent
session after interruption**, as specified below. This is not an initial-version
compromise. A separate holder is optional future architecture, not a prerequisite.

Full shutdown is a two-step plan/confirm operation bound to current epoch and
active run set. Show affected runs, PTY close/SIGHUP/termination/work-loss risks;
allow cancel. If the run set changes, reject the old plan. New launches cannot
race a committed shutdown. Window close is never this operation. OS logout and
shutdown flush are best effort, not guaranteed safe termination.

Defer daemon/protocol update while runs exist; notify on update attempts and at
24 hours without forced stop. Auto-start is opt-in, default off, and starts no
agents. Installing a launchd job or altering host login settings requires an
explicit scoped operation; test the descriptor in isolation by default. Sleep or
wake/gap invalidates interaction freshness. Require new causally bound evidence,
not a heartbeat, to restore state. Native sleep/wake behavior needs its own real
macOS evidence; synthetic clock tests prove only the reducer contract.

## Safe session resumption after interruption — product specification

The maintainer explicitly selected session-level recovery on 2026-11-03.
Interruption may end the old PTY/process. Traceary must offer an explicit recovery
action for each host/profile certified for recovery, using that host CLI's native
resume facility and the **same verified native conversation**, not a new chat or
a synthesized replay. The previous process/terminal contents and unfinished tool
execution are not restored. This specification is not limited to an initial release.

Persist an owner-private, versioned recovery record when native identity is
verified, before advertising the run as recoverable: canonical host/version/profile
identity, native conversation ID, canonical project/worktree/store, old run and
generation, immutable launch policy, observed child/process identity and recovery
status. Write atomically with file and parent-directory durability appropriate to
macOS; no raw prompts, terminal bytes, secrets or arbitrary resume command strings.
A record is correlation, not authorization. Recovery support is certified separately
from live interaction state; an unknown waiting state alone need not prevent a
resume whose session identity and exclusivity can be independently proven.

The operator selects the interrupted run and confirms its recovery target. Under
a per-native-session exclusive reservation, verify the native session still
exists, the original canonical worktree/store/profile is unchanged and usable,
the exact host/version resume contract is supported, and the previous execution
is definitively no longer active. A saved PID, absence of its parent, elapsed
time or an orphan label alone cannot prove that no descendant or external host
process still owns the native session. Use owned process identity and host-native
exclusivity evidence where available; otherwise report blocked/unknown rather
than guess, adopt or kill an existing process. Concurrent or duplicate recovery
requests cannot start two owners of the same native session.

Resume invokes the configured real CLI directly through its versioned, typed
resume adapter, retaining host-owned authentication/approval and fixed target
constraints. Allocate a fresh run/generation/epoch, link it to the interrupted
predecessor, and bind it anew; old callbacks, input receipts and controls remain
stale. A recovery request gets a bounded durable intent/result before reporting
success. If recovery itself crashes or delivery is ambiguous, reconcile ownership
and native identity; do not launch again automatically. Confirm actual resumption
of the recorded native session, not merely successful process spawn.

Never resend a pending prompt, partial input, approval answer or interrupted tool
command automatically. Resume restores the host's committed conversation history;
it does not prove an earlier shell command or external side effect did not finish.
Show interruption and uncertain deliveries to the operator before further input.
Missing/corrupt native history, unverifiable identity, unsupported version, changed
worktree/profile or ambiguous old-process liveness produce an actionable recovery
error; no implicit new conversation or alternate host/model/billing fallback.
Preserve the recovery record and existing history. Do not delete a worktree or
move the session to a replacement directory to make recovery appear successful.

Each supported recovery host/version must pass a real interrupted-session →
explicit resume test demonstrating the same native conversation and original
worktree, a new generation and no input replay. Also test concurrent resume,
old-process-still-live, PID reuse, missing history/record, changed target/version,
partial input and crash during recovery. Fixture-only or unavailable resume stays
unverified/unsupported, not certified. Muse native work stays deferred. Native
resume limitations must be visible; recovery is not guaranteed against loss of
the host's own persisted history or filesystem.

## Capture, native witnesses and projection

History is written by the existing CLI pipeline; optional live delivery cannot
prevent persistence, double-record hooks, alter decision stdout or delay a hook
past its total optional budget. Global and per-run native hooks must compose
without replacing user handlers; untested composition is unknown. No global
config overwrite or host trust/auth changes are authorized.

Adapters are exact-host-version contracts. A launch descriptor is a correlation
hint; accept live observations only when the adapter can verify independent
native session/turn/generation evidence. Env propagation, callback arrival time,
PID parentage and matching cwd alone are insufficient. Ambiguous causality is
unknown even if the frame was recently received. Receipt monotonic time limits
projection lifetime, not source-to-daemon delivery age. Duplicates do not renew
TTL; stale waiting can be retained as a labelled last observation, never current
wait or authority. Do not infer interaction from terminal bytes or silence.

Certify Codex, Claude Code, Antigravity and Grok independently through host-specific
leaf issues under #2468. This is not a promise all states can be supported. A host
without a reliable witness remains unbound/unknown, with evidence explaining the
limit. Muse native testing and capture additions remain deferred; Gemini/Kimi
are outside initial native certification. Unsupported states do not block IPC/PTY
foundation work or become certified by a synthetic fixture. No hidden engine or
billing fallback is allowed after host authentication failure.

## Registry, history and maintenance

#2472 supplies the single headless registry backend; Desktop #2431 consumes that
same service. Canonical identities, duplicate/missing/foreign paths, explicit
export/delete and admission bounds belong to Registry, not renderer state.
Deleting metadata never deletes a Git worktree/branch or core store. Profile
registration is a separate explicit operator operation, not a renderer arbitrary
shell facility. Snapshot profile configuration for each run; edits do not rebind it.

Runtime metadata is owner-private and separated from the existing SQLite store;
its versioned envelope contains only safe registry/run/receipt summaries.
Retain uncertain receipts and orphaned records until explicit operator cleanup;
reject overflow instead of silently evicting them. Atomic replace must validate
owner, no-follow path and size before commit and preserve the previous good file
on failure. Newer metadata schema is unsupported, not auto-migrated by old code.
Export/delete cancel leaves user data unchanged. Do not persist PTY output.

Core history uses existing query/usecases and lease-aware connections. Post-commit
notifications are hints; resnapshot after loss. The current datetime cursor is
not a durable commit-sequence proof. No new outbox/sequence/core schema is added.
Bound readers, release them for upgrade/compaction and retry with visible busy
state while PTYs continue. Do not introduce another spool drainer: existing
receipt/idempotency/claim rules retain ownership. If a new cursor or drainer is
needed, split a schema/claim compatibility checkpoint rather than extending R0.

## Threat model and error contract

Untrusted inputs include terminal escapes, hook content, IPC frames, profile
references, filesystem paths and saved metadata. Renderer trust is not assumed.
Output is data, never executable markup or evidence of permission. Do not execute
OSC clipboard/link operations automatically. Log only minimized diagnostic codes
and safe IDs, never raw input, argv, secret-bearing environment or native payload.

| Condition | Required observable result |
|---|---|
| Unsafe socket/peer/path/version/schema | Rejected/unsupported; no mutation or foreign unlink |
| Missing daemon/optional timeout | CLI history and decision contract unchanged; live unavailable |
| Stale epoch/generation/foreign target | Reject control; reducer unchanged or unknown |
| Partial write or receipt gap | delivery-unknown; no automatic resend |
| Queue/connection/metadata overflow | busy/limit error, no silent eviction |
| Output cursor gap | Explicit missing byte interval and fresh snapshot |
| Unsupported host/witness/order | unbound/unknown, no invented wait |
| Maintenance | visible busy/retry, released store lease, PTY unaffected |
| Client loss | detach, no duplicate child |
| Daemon loss | interrupted/orphan/unknown; explicit same-session recovery after ownership checks, no PID adoption |

No security claim is made for a compromised same-UID owner. If OS peer/path
checks cannot be established, live delivery/control is unavailable rather than
silently downgraded. This is distinct from unsupported interaction evidence.

## Behavior tests and TDD order

| Scenario | Red test / Green boundary | Level |
|---|---|---|
| UI has no adopted framework | Headless dependency graph contains no UI gate | docs/graph |
| Foreign/symlink/oversize/slow socket | Whole request rejected with zero mutation | real Unix IPC + fuzz |
| Same cwd, different runs/generations | No cross-target input, binding or stop | domain + real PTY |
| Duplicate launch/cancel/start failure | At most one owned child, honest receipt | runtime fixture |
| Slow consumer/output storm | Bounded memory, drained child, explicit gaps | byte-count integration |
| Detach/reattach/resize storm | Same generation; exact byte/ack identities | PTY fixture |
| Partial input/crash/duplicate | Unknown receipt, no resend | fault injection |
| Duplicate-after-expiry/reorder/wake | unknown, no revived wait/duplicate Attention | fake monotonic clock |
| Interrupted session / concurrent or ambiguous resume | Same native conversation, new generation, no duplicate owner/input replay; ambiguity blocks | native + fault |
| Daemon absent, global/per-run hooks | Same history/receipt/stdout, no double event | CLI + host-version native |
| Store maintenance/late history insert | Release lease, resnapshot, PTY survives | real SQLite |
| Shutdown cancel/changed run set | No hidden stop; stale plan rejected | process fixture |
| Native sleep/wake/approval/witness | Evidence per exact host/version; unrun stays unrun | native macOS |

Order: pure identity/projection Red/Green, registry persistence, Unix IPC, real
PTY/control/fault fixtures, existing CLI capture/history integration, then exact
host-version probes and same-integrated-SHA headless QA. Unit fixtures never pass
UI/native/manual tests. Use external test packages and public observable APIs.

## Delivery, validation and rollback

| Issue | Scope / dependency |
|---|---|
| #2471 | R0 design/docs/review/human acceptance; no UI predecessor |
| #2472 | Shared registry backend after R0 |
| #2460 | Owner-scoped IPC after R0 |
| #2461 | PTY launch after IPC + headless registry |
| #2462 | Stream/detach/reconnect after launch |
| #2463 | Explicit target-bound control after stream/launch |
| #2464 | Lifecycle/shutdown/update/wake after control |
| #2478 | Durable explicit same-session recovery after lifecycle + host resume adapters |
| #2465 | History/maintenance after IPC/launch |
| #2466 | Optional observation intake after IPC/launch |
| #2467 | Pure projection after launch identity contract |
| #2468 | Host-version leaf certification after intake/reducer; Muse stays deferred |
| #2470 | Same-SHA headless integration after applicable runtime and adapter work |

#2432 and #2433 are aggregate tracking issues, not a completion cycle. D4 consumes
accepted launch foundation #2461, not completed D3 including #2470. UI D0/D1/D2
are downstream consumers, not ancestors of headless runtime. Each implementation
leaf gets its own branch/draft PR and closes exactly that leaf; parents stay open
through their required release/integration policy.

Commit uses `scripts/test-select-staged.sh`; docs use `make docs/check` and
`git diff --check`. Implementation validates affected packages and reverse
dependencies, fresh final-head full CI/lint/security and wave E2E on the integrated
SHA. Record exact commands/input SHA/results/log hashes/unrun gates. For
performance, fix workload/machine/limits before measurement; report all process
CPU/RSS, bytes and gaps, not UI event-loop estimates. No native result is claimed
by this docs PR. Independent High-risk design review and explicit human acceptance
are required before product runtime implementation. Architecture PR Ready/merge
is human-operated, not automatically authorized by the general delivery goal.

Rollback disables new launch/control/live delivery, presents affected active runs
and cancel before daemon shutdown, preserves existing CLI/hooks/history/Memory
and user metadata. Do not force-remove worktrees or automatically kill/resume orphans.
Keep the explicit, validated recovery path available when disabling live projection.  New core
schema remains out of scope; any future migration needs a separate backup/restore
checkpoint. If R0 is rejected, product runtime remains unimplemented; UI D0 is
not silently accepted in its place.
