# Desktop runtime observations and the macOS D0 checkpoint

[日本語](2026-10-18-desktop-runtime-observations.ja.md)

- Status: **Proposed; D0 incomplete; human-gated architecture decision**
- Issue: [#2424](https://github.com/duck8823/traceary/issues/2424)
- Parent: [#2414](https://github.com/duck8823/traceary/issues/2414), which stays open
- Baseline: `c6c4d2dbb8790be33a4d6903a9b9e833cbdb2af5`, v0.53.0 already released
- Scope: macOS; Codex, Claude Code, Antigravity CLI, Muse Code, Grok Build

## Requirement summary

The Desktop must show the correct worktree, terminal, history and attention target
without mistaking history for current process or interaction state. Existing CLI
hooks remain useful when Desktop is absent. Agent authentication, subscription,
permissions and approval decisions remain with the real agent CLI.

No agent wrappers, PATH shims, aliases, shell-profile rewrites, mandatory
`traceary session run`, credential capture, terminal-text state inference,
automatic agent resume, core schema changes, or product Desktop implementation
are authorized by this ADR. Gemini/Kimi history remains readable, but their
interaction state is not part of the first certification wave. Architecture
visualization, cloud synchronization, remote relay and GitHub review submission
remain out of scope.

## Alternatives and proposed decision

| Approach | Benefit | Rejection or adoption condition |
|---|---|---|
| Wrapper plus hooks, as in Superset | Reliable launch correlation where supported | Reject wrappers; retain the idea of explicit launch identity |
| Pane text/hash classification, as in Claude Squad and agent-deck | Can fill missing host notifications | Reject as authoritative state; rendered text is untrusted and version dependent |
| Native hooks plus scoped local observations | Reuses Traceary capture and real CLI authentication | **Proposed**; only states supported by versioned evidence are projected |
| Host SDK/app-server for every interaction | Potentially structured input and approval requests | Follow-up per host; not a common five-host contract and not an implicit API-billing switch |

Superset itself documents host coverage differences; its UI is not proof of
universal waiting-state support. cmux separates Codex permission-hook telemetry
from actionable approval requests. Conductor's public UI documentation does not
establish its internal state classifier. These are implementation references, not
Traceary certification evidence. See [sources](#sources).

## Conceptual model

| Concept | State and behavior | Invariant |
|---|---|---|
| Session | Existing history grouping and host conversation identity | Never owns live running/ended state |
| Run | Managed execution with canonical store/worktree/host identity | Session ID and PID alone do not authorize operations |
| Generation | Fresh launch/restart identity; includes daemon epoch | Old generation cannot overwrite a new run |
| Binding | Immutable launch descriptor and validated native-session association | Ambiguous or foreign associations remain unbound |
| Observation | Versioned host evidence with source, receipt time, host time if present, native turn/request IDs and event identity | Missing ordering evidence is not invented |
| Projection | Pure interpretation of a run's accepted observations | Produces display state only; never creates any authority |
| Review snapshot | Immutable old/new content and anchor identity | Changed content requires explicit reanchoring before submit |

Keep five axes independent: process (`launching/running/exited/unknown`),
interaction (`working/waiting_input/waiting_approval/unknown`), capture health,
Desktop connection, and committed history. An additional observation label such
as `approval_requested` is telemetry, not a claim that a human prompt is visible.
`Stop` is a turn boundary, not process exit or safe-to-paste evidence.

## Responsibilities and boundaries

| Owner | Owns | Does not own |
|---|---|---|
| Native host/version adapter | Native event interpretation and documented limitations | Cross-host inference or permission decisions |
| Existing Traceary CLI | Redaction, event persistence, receipt/spool semantics; optional bounded observation delivery | PTY lifetime or generic Desktop RPC |
| Go daemon runtime | Child process/PTY, run generations, operation targeting, live projection and subscriptions | Session-domain lifecycle or model authentication |
| Existing application services | History, audits, memory candidates/adoption, refinement and coverage rules | Desktop layout or live PTY buffering |
| Native shell | Window/menu-bar presence, user actions, typed IPC | Arbitrary renderer filesystem/shell access |
| Renderer | Pane layout, terminal, diff, review drafts and evidence labels | SQLite, credentials, raw socket, arbitrary command execution |

The existing `presentation/cli/session_run.go` is a one-shot execution contract,
not the interactive PTY owner. Do not extend it into a mandatory launcher.
`application/types/session_summary.go` is a history read model; timestamps in it
do not prove liveness. Existing usecase granularity is preserved: memory and
refinement services retain their current object-level operations. No new core
usecase is implemented by this ADR.

## Proposed no-wrapper observation contract

### Launch and binding

The daemon launches the explicitly configured real executable with argv, cwd and
environment. It does not prepend a shell or replace the agent name on PATH.
Native hook handlers call a narrow Traceary observation intake; its command name
and schema remain proposed until their own public-contract implementation PR.

Environment is a routing hint, not authorization. Muse's local echo probe shows
that an arbitrary launch variable does **not** reach its hooks. Per-invocation
native hook configuration carrying an owner-private binding descriptor path is
a candidate only, not adopted until each host passes native validation; never
replace global configuration. The proposed descriptor binds store,
canonical worktree, host executable/version, run, generation and daemon epoch.
Native session/turn IDs are associated after validation, not trusted as secrets.
Descriptor paths and public run IDs do not grant input/stop authority.
If a host cannot propagate a unique validated binding without a wrapper, expose
its history and unbound observations only; managed live interaction remains unknown.
Do not use cwd matching or PID ancestry alone as a fallback authorization mechanism.
Where the CLI supports choosing a fresh native session ID before launch, that ID
can also provide correlation, but never authentication. Reusing a conversation
on resume does not identify a generation: without an independent generation
witness, delayed callbacks remain ambiguous and cannot update live state.

### Local transport

Propose a user-owned Unix socket under
`$XDG_STATE_HOME/traceary/desktop/runtime/daemon.sock` (default
`~/.local/state/traceary/desktop/runtime/daemon.sock`). Require an absolute path,
an OS-valid path length, owner UID, non-symlink directories, directory mode 0700
and socket mode 0600. Refuse unsafe existing paths; never unlink a foreign socket.
Use macOS peer identity (`getpeereid`/equivalent) plus a version handshake and
run binding. Same-UID processes are not fully isolated: this socket is not a
security boundary against a malicious agent running with the same user privileges.

Observation intake permits observations only, never terminal input, approval
decisions, memory mutations or process control. Renderer IPC is a separate
allowlisted surface with sender/frame validation, schema validation and resource
binding. No generic shell/file/socket method is exposed.

Proposed bounds before implementation: observation frame 64 KiB; 250 ms total
optional delivery budget measured on the CLI monotonic clock; daemon-owned
queue bounded by both 256 frames and 8 MiB. The 60-second projection TTL is
defined below; it is not a guarantee about source-to-daemon delivery age.
Overflow/disconnect makes capture health degraded and interaction unknown;
it does not block the agent, overwrite hook decision stdout, or suppress existing
history/spool delivery. These values are proposals, not current CLI guarantees.

### Ordering, reconnect and error contract

| Condition | Proposed observable result |
|---|---|
| Missing daemon | Existing history capture continues; live delivery unavailable |
| Unsafe socket, foreign UID or binding | Reject observation; no input/stop capability |
| Unknown protocol or host version | Unsupported/unknown with reason; no guessed mapping |
| Duplicate event identity | Idempotent projection and notification deduplication |
| Old epoch/generation or expired evidence | Reject live transition; preserve eligible historical capture |
| Reversed causal order | Ignore proven older evidence; ambiguous order yields unknown |
| Missing/expired subscription cursor | New snapshot with explicit gap, not a claim of lossless resume |
| Store maintenance/lease conflict | Release read connections and retry boundedly; PTY continues |
| Unknown input delivery after crash | Show indeterminate; never automatically resend |

Transport receipt sequence is not native causal order. Host turn/request IDs may
only group events; they do not by themselves order concurrent callbacks. Do not
resolve contradictions solely by wall clock. Synthetic reducer tests must be
labelled synthetic; they do not certify host ordering.

Use a daemon-epoch snapshot and bounded in-memory cursor for live state.
Historical replay never resurrects a current waiting/working state. Existing
`presentation/cli/tail.go` has a timestamp cursor and is not a durable commit-order
stream: delayed historical inserts require separate reconciliation. Start with
bounded read refresh and an explicit gap indicator; a guaranteed durable cursor
or outbox requires a separate schema/compatibility checkpoint.

`doctor --json` is an explicit diagnostic, not a heartbeat: it can initialize the
store and probe hosts. A resident daemon must not hold shared store leases while
idle, through upgrade, or through compaction. Reuse existing receipt ownership;
do not introduce a competing spool drain without claim/lease design.

## Host capability matrix and evidence classification

`D` means an official moving documentation source; `N` means an observed native
binary result; `F` means synthetic/local echo fixture. None of these classes alone certifies
interactive behavior. Each cell cites the source key below; unknown is deliberate.

| Host and installed version | Working evidence | Input-wait evidence | Approval-wait evidence | Turn boundary | Process exit |
|---|---|---|---|---|---|
| Claude Code 2.1.302 (N0) | Prompt/tool hooks (D1); native unknown | Notification subtype (D1), delayed; native unknown | `permission_prompt` (D1); request alone insufficient; native unknown | Stop (D1); native unknown | Child wait only; hook alone unknown |
| Codex 0.163.0 (N0) | Prompt/tool hooks (D2); native unknown | Dedicated proof unknown (D2) | PermissionRequest is telemetry, human visibility unknown (D2,D6) | Stop (D2); native unknown | Child wait only; hook alone unknown |
| Antigravity CLI 1.4.0 (N0) | Invocation/tool hooks (D3); native unknown | `fullyIdle` alone does not prove input acceptance (D3) | Unknown (D3) | Stop plus `fullyIdle` (D3); native unknown | Child wait only; hook alone unknown |
| Muse Code 1.4.10-R6427.1 (N0) | PreLLMCall observed in echo only (F1,D4) | Unknown; Stop does not prove it (F1,D4) | Notification documented (D4); native unknown | Stop observed in echo only; Meta-provider behavior unknown (F1) | SessionEnd not observed in F1; child wait only |
| Grok Build 1.0.62 (1474f9cbdd91) (N0) | Prompt/tool hooks (D5); native unknown | `idle_prompt` documented on main, installed-version behavior unknown (D5) | `permission_prompt` documented on main, native unknown (D5) | Stop documented (D5); native unknown | Child wait only; hook alone unknown |

N0: real binary `--version` and `--help` in an empty project/HOME; not interaction
certification. The configured `claude` on PATH is an existing print-routing
wrapper; D0 resolved and used `~/.local/bin/claude` directly without modifying it.
Muse/Grok installation and existing login were confirmed by the maintainer;
this does not certify hook delivery. No login automation or credential copying.

F1: `muse exec --provider echo --preset empty --disable-shell --disable-write
--disable-web-tools --disable-reminders --no-foreign-personal-context
--no-session-log --approval-mode never --max-model-steps 1`, with ephemeral
`-c hooks=...`, synthetic prompt and empty HOME. Observed SessionStart,
UserPromptSubmit, PreLLMCall, PostLLMCall and Stop; each lost the synthetic
environment binding. Turn IDs existed after SessionStart. SessionEnd was
registered but not observed. This is local echo evidence, not Meta-provider
operation, human input, approval handling or a claim that SessionEnd never fires.

F2: two concurrent echo runs, each with its own empty HOME/project and a fixed
synthetic binding literal in its per-run hook command, produced six observations
(SessionStart/UserPromptSubmit/Stop per run). Each binding mapped to one distinct
native session; neither recovered the launch environment variable. This proves
that native per-invocation hook arguments can carry correlation in this fixture,
not that a public literal authenticates a run or that Meta-provider behavior is
certified. It is an alternative to relying on inherited environment, not a
wrapper or a permission bypass.

The Grok build suffix could not be resolved to a commit in the public repository
(GitHub returned 422). D5 therefore remains moving documentation, not source
proof tied to the installed binary.

## Desktop metadata and lifetime

Separate Desktop registry/layout/review-draft metadata from the existing store.
Propose an owner-private store with an independent schema version and explicit
export/delete UI. Initial bounds: 100 projects, 1,000 worktrees, 256 active run
records, 100 review drafts of at most 1 MiB each; metadata envelope 128 MiB.
Exceeding a bound rejects the new mutation with a visible reason; never silently
delete user drafts. Counts and limits need acceptance during the design review.
Raw terminal output is not durably recorded by default: per run, bound in-memory
scrollback and queued output and mark dropped display data explicitly.

Window close detaches, leaving the daemon/PTY alive; reopening attaches to the
same managed generation. Menu-bar presence and the headless daemon have separate
lifetimes. A quit command lists active runs and permits cancellation. Stop only
the revalidated selected run, not a remembered PID. UI crash must not kill PTYs.
Daemon crash is different: full PTY recovery is not an initial guarantee; show
unknown/orphaned and never automatically kill or resume a discovered process.

Updates cannot implicitly stop agents: defer daemon/protocol updates while runs
are active or request explicit confirmation. Login auto-start is opt-in/off by
default and never auto-starts agents. Sleep/wake invalidates freshness until
reconciliation. OS shutdown/logout is best effort, not guaranteed flush/recovery.
Deduplicate Attention/OS notifications by accepted state transition. Notifications
default to metadata only, excluding prompt/code/audit/memory and private titles.
Permission denial for OS notifications leaves in-app Attention usable.

## Threat model

Treat terminal escape sequences, file/diff content, hook payloads, Markdown and
audit output as untrusted. Restrict navigation, links and clipboard actions;
disable automatic OSC clipboard writes. Electron requires sandboxed renderers,
context isolation, Node disabled, restrictive CSP and sender checks. Wails needs
equivalent narrow bindings; a native WebView is not automatically a safe bridge.
Run/store/worktree/snapshot targeting is rechecked at action time. Observation
freshness never grants approval or implies the host understands a delivered review.

## Technology spike and adoption checkpoint

Fixed before measurement on 2026-10-18: macOS 26.5 arm64, 16 GiB RAM, 10 physical
cores; Electron 44.9.0, Wails v2.16.0, xterm.js 6.0.0, Monaco 0.57.0, esbuild 0.28.2.
Use the same bundled frontend, three 80x12 terminals, 1,000-line scrollback,
2 MiB in-flight cap per terminal, 1 MiB/s per terminal for 60 seconds, 200 resizes,
and 100,000 generated diff input lines with an explicit 5,000-line preview.

Thresholds frozen for the recorded protocol (not adjustable after results): startup descriptive p95 <=3 s over five launches,
interaction p95 <=100 ms, idle mean <=5% of one logical CPU over 60 s, total
application RSS excluding agents <=1 GiB. Five samples do not establish
statistical confidence; nearest-rank p95 at n=5 is the maximum (in these
trials, the initial launch), not proof of a cold filesystem cache. Record process trees including WebKit helpers; renderer
event-loop delay is not a substitute for native input latency. Package size must
compare equivalent packaged builds, not one executable against an entire runtime.

**Implementation decision: no-go; neither framework is adopted.** D1–D10
remain blocked until the missing native gates are resolved and a new explicit
adoption decision is accepted. This negative checkpoint is not completed D0. Native Japanese IME commit/cancel, TUI/Unicode,
clipboard, VoiceOver, multiple monitors, close/reopen and menus, sleep/wake and fault tests
are mandatory. A successful build, renderer screenshot or synthetic throughput
does not pass them. Spike code stays outside production packages and is not
merged as product implementation. Do not revise failed thresholds after measuring.

### Local measurements, not acceptance evidence

The [sanitized evidence](evidence/desktop-d0-2026-10-18.json) contains fixed input
hashes, native/echo classifications and measured values. Both native applications
built and rendered the same xterm/Monaco fixture. Wails needed scoped
`CGO_ENABLED=1` and `CGO_LDFLAGS='-framework UniformTypeIdentifiers'`; these changed
neither product configuration nor framework source. Package generation was local
only; there was no distribution.

| Diagnostic | Electron | Wails v2 |
|---|---:|---:|
| Packaged fresh-process ready descriptive p95, 5 launches | 2,724.65 ms | 1,483.88 ms |
| Uncompressed regular-file bytes in equivalent fixture app bundle | 305,440,958 | 11,240,190 |
| Direct-binary trial: delivered terminal bytes / required 188,743,680 | 188,743,680 | 20,761,881 |
| Direct-binary trial: resize count / required 200 | 200 | 66 |
| Direct-binary renderer event-loop delay p95 (not input latency) | 1.10 ms | 1,072 ms |
| Packaged trial: delivered terminal bytes / required 188,743,680 | 88,395,012 | 181,509,198 |
| Packaged resize count / required 200 | 200 | 200 |
| Packaged renderer event-loop delay p95 (not native input latency) | 2.30 ms | 8.00 ms |

**The load fixture was not fully delivered by either packaged trial.** Native
focus/App Nap/timer behavior was not controlled, and a read-only CUA window
observation occurred during the Electron packaged trial. The direct and packaged
results differ substantially. These runs cannot establish a framework speed
ranking or a passing 3 MiB/s terminal path. The generator uses timer-driven writes,
not a real PTY stream; the next trial needs a controlled real-PTY producer and explicit
foreground/background cases. Do not reduce the workload to declare a pass.

`ps` samples cover the app and discoverable descendants only; detached OS WebKit
helpers may be missing. Its reported CPU percentage is EWMA, not integrated CPU
time. Neither these samples nor the main-function readiness phase certify the
total CPU/RSS/native-input gates. External readiness uses process-to-report
monotonic time, with 10 ms polling for quick launches; filesystem caches were not
flushed. Native screenshots confirmed drawing only, not IME or VoiceOver.

### Revised native PTY fixture

A protocol fixed before this second trial retained the original three terminals,
1 MiB/s per terminal for 60 seconds, 2 MiB source queue per terminal, 200 resizes
and bounded 5,000-line diff preview. A local broker reads three real raw PTYs;
the renderer awaits each bounded read and xterm write acknowledgement. Both
direct native variants delivered all 188,743,680 bytes with zero reported drops,
200 resizes and source EOF, in 60,281.4 ms (Electron) / 60,269 ms (Wails).
Terminal write-ack p95 was 22.60 ms / 10.00 ms, respectively; this is not native
input latency or a fair framework ranking. Same-UID/path/protocol/UTF-8 smoke
tests passed twice (two cases); cross-UID resistance was not tested.

This fixes the earlier timer-producer delivery defect without reducing the load.
It does not replace equivalent packaged reruns, complete process CPU/RSS, native
TUI/IME/clipboard/accessibility or crash/sleep-wake tests. A repeated CUA launch
of the isolated manual fixture returned `timeoutReached`, so no manual pass is
claimed and no alternate GUI driver or OS-security workaround was used. A read-only
code-signature check subsequently found an incomplete signature on the copied
self-authored manual app. Normal ad-hoc development signing and strict signature
verification succeeded, but CUA still returned `timeoutReached`. The cause of
the remaining launch failure is unknown; no OS protection was disabled.

## Behavior tests and TDD plan

| Given / when | Required observable outcome | Level |
|---|---|---|
| Old generation reaches a new run | No new-run mutation | Pure reducer, Red first |
| Duplicate or reversed events | No duplicate Attention; unknown on ambiguous causality | Reducer + adapter |
| Stop with background work | No fabricated exit or safe input state | Native host |
| Permission hook auto-resolves | No false human-wait claim or changed decision | Native host |
| Daemon absent or unsafe/foreign socket | History continues; optional delivery rejected | CLI integration |
| Env removed / unknown version / foreign run | Unbound/unknown; input and stop refused | Binding + native |
| Maintenance and tail refresh overlap | Readers drain/retry; PTYs keep running | Store integration |
| Reconnect with replay or cursor gap | Snapshot/gap visible; no stale waiting resurrection | Fault E2E |
| Review target/content changes | Stale anchor rejected; draft retained | Unit + E2E |
| Input send crashes midway | Indeterminate receipt; no automatic resend | Fault E2E |

Implement pure transitions and binding invariants first in an isolated spike,
then adapters, IPC and native vertical tests. Keep host-version variation in
adapters, not a giant orchestration switch. Do not invent a generic plugin system
for hypothetical hosts. The commit selector, final-head CI and integrated wave
E2E remain governed by CONTRIBUTING; docs-only checks do not certify native QA.

An isolated executable specification ran 16 synthetic reducer tests successfully.
Its expiry regression first failed (`working` remained after a stale duplicate),
then passed after checking freshness before duplicate handling. These tests do
not cover the real daemon, native causality, peer identity, sockets or store leases.

## Implementation issue plan and rollout

Planning sub-issues exist under #2414 but remain explicitly blocked on D0
acceptance and a framework decision. Their creation is not implementation
approval or native certification; no requirement is silently waived.
Every issue has one owning writer, a dedicated branch/draft PR, positive/negative
acceptance, exact validation commands and a rollback section.

| Package | Dependencies | Acceptance focus / rollback |
|---|---|---|
| [D1](https://github.com/duck8823/traceary/issues/2430) Native read-only shell | D0 | Approved panes; bounded Audit/History/Memory; disable UI only |
| [D2](https://github.com/duck8823/traceary/issues/2431) Project/worktree registry | [D1](https://github.com/duck8823/traceary/issues/2430) | Canonical paths, missing/duplicate targets; retain existing worktrees |
| [D3](https://github.com/duck8823/traceary/issues/2432) Managed daemon/PTY (split launch and reconnect) | D0,D2 | Real CLI, detach/reattach, bounded output, explicit stop; no forced cleanup |
| [D4](https://github.com/duck8823/traceary/issues/2433) Host observations/Attention (split reducer and adapters) | [D3](https://github.com/duck8823/traceary/issues/2432) | Five versioned hosts, unknown, ordering/binding/fault tests; disable observation |
| [D5](https://github.com/duck8823/traceary/issues/2434) New worktree flow | D2,D3 | Branch/path collision and partial failure; no force delete |
| [D6](https://github.com/duck8823/traceary/issues/2435) Immutable diff | [D2](https://github.com/duck8823/traceary/issues/2431) | Staged/unstaged/untracked, rename/delete/binary/large files; retain snapshots/drafts |
| [D7](https://github.com/duck8823/traceary/issues/2436) Review and safe Submit | D4,D6 | Stale anchors, target revalidation, manual fallback, uncertain receipts; disable sending |
| [D8](https://github.com/duck8823/traceary/issues/2437) Memory/refinement mutations | [D1](https://github.com/duck8823/traceary/issues/2430) | Explicit adoption, scope/source/coverage, existing recovery; disable Desktop writes |
| [D9](https://github.com/duck8823/traceary/issues/2438) Native QA/internal beta | D4,D5,D7,D8 | Security/native E2E/fault and compatibility evidence on integrated SHA; no automatic publication |
| [D10](https://github.com/duck8823/traceary/issues/2439) More OS/hosts | [D9](https://github.com/duck8823/traceary/issues/2438) | Separate OS/version certification; unsupported stays unknown |

First technical slice: register existing worktree -> real CLI -> terminal ->
detach/reconnect -> evidence-labelled state. Then add diff and review drafts.
Keeping structured sending out of the first beta would change the parent's gate;
it is a proposal requiring explicit approval, not an adopted scope reduction.

Rollback stops/disables optional Desktop features while keeping CLI/hooks/history/
memory intact. No existing store migration is introduced. A future required
commit cursor/schema change gets its own compatibility checkpoint and full-store
backup/restore plan; old binaries are not assumed to read a new schema.

## Normative clarifications from independent review

These are proposed contracts, not implemented guarantees or D0 adoption.

### Freshness, identity and attribution

- Delivery budget (250 ms) bounds only optional CLI work. Daemon receipt records
  its monotonic time, not a comparison of host and daemon wall clocks. Host time
  is diagnostic. Receipt time cannot prove how long a callback was delayed. A
  callback lacking sufficient generation/turn/causality evidence stays unknown.
- Live interaction has a conservative 60-second TTL from the first accepted
  nonduplicate observation, for working **and waiting** states. Duplicates never
  refresh it. At expiry show unknown; preserve a separately labelled stale
  “last observed waiting” Attention item and its timestamp, not a current wait
  or authorization. Long-lived waiting cannot remain authoritative without new
  evidence. This explicitly matches the synthetic expiry regression.
- Later accepted observations, process exit, changed epoch/generation, sleep/wake
  and cursor gaps invalidate prior projection. Reconciliation means unknown
  until a new accepted, causally bound native observation; history replay,
  heartbeat and mere process liveness do not restore it.
- Adapter event identity must include native event/request identity plus run,
  generation and epoch, where provable. Content hashes alone cannot collapse
  repeated turns. If identity/order cannot be distinguished, mark ambiguity
  unknown rather than inventing deduplication. Unbound observations never update
  a run/pane or trigger its Attention. Projection never creates authority.

### Binding and hook composition matrix

| Host | Environment / per-run configuration / prechosen session binding | Hook composition |
|---|---|---|
| Claude | Native unknown | Native unknown |
| Codex | Native unknown | Native unknown |
| Antigravity | Native unknown | Native unknown |
| Muse | Environment lost; per-run literal correlated in F2 echo only; native provider unknown | Native global/per-run merge unknown |
| Grok | Native unknown | Native unknown |

Unknown binding permits history/unbound display only, not managed live
interaction. Prefer one existing handler receiving binding over adding a second
recording hook. Test additive/override semantics per host and reject suppression
or duplicate history capture. D4 certifies exact host versions initially; an
auto-update loses certification until re-probed. No compatible range is inferred.
The user may explicitly select an existing custom executable by absolute path;
Desktop does not silently unwrap, replace or create it.

### Transport and input safety

Both CLI and daemon verify the connected peer UID. Validate every path component
from the user-owned state root; reject symlinks/unsafe ownership or permissions.
Create the private runtime directory with 0700, bind under umask 077, revalidate
path/socket ownership and inode after bind/connect and reject detected races.
Component checks alone are not a proof against same-UID TOCTOU; implementation
needs descriptor-relative/no-follow operations and native negative tests. The
private directory and peer identity are primary controls, not socket mode alone.
An overlength Unix path makes live delivery unavailable; history capture continues.

Frames contain derived, validated metadata only: protocol/source/host version,
binding/generation/epoch, native event/turn identity, receive/diagnostic times and
state/evidence labels. Never send raw prompts, tool output, audit bodies, terminal
content or credentials. Oversize frames are rejected whole, never truncated.
Queue overflow drops optional observations with explicit degraded/unknown state;
the short-lived CLI has no durable runtime queue and never replaces existing
history/spool receipts with live transport receipts.

Review Submit targets the selected agent, not GitHub. Untrusted text must reject
or visibly neutralize C0/C1 controls (only LF and TAB may be preserved as text),
including ESC and bracketed-paste terminators. If a host cannot safely delimit
multiline paste, use a preview/manual transfer without automatic Enter. Every
send is a revalidated explicit user action; observed waiting never grants input
or approval authority. Test control injection, target changes and uncertain
partial sends; no automatic resend.

Executable/argv/env profiles are changed only through explicitly confirmed
native host configuration; the renderer references an allowlisted profile ID,
not arbitrary commands. D9 must verify app/update signatures, notarization and
hardened-runtime policy and Electron fuses before distribution. Local ad-hoc
fixture signing is not distribution certification.

### Lifetime, tests and implementation traceability

UI quit is detach. Full daemon shutdown requires listing affected runs and explicit
confirmation: closing the PTY master may deliver SIGHUP, terminate agents or lose
their controlling terminal. Crash can lose work; “orphaned” is not recovery. D3
must evaluate a separate PTY holder without promising it now. Never auto-kill or
resume survivors. Updates may remain deferred while runs exist; notify in-app
after 24 hours and on each explicit update attempt, with cancel/export/explicit
stop choices, never a forced timeout. Auto-start stays opt-in.

| Given / when | Required result | Level / owner |
|---|---|---|
| Daemon loses PTY master | Possible SIGHUP/work loss is reported; no guessed recovery | Native fault / D3 |
| Active run during update | Deferred, notification at 24h, no automatic stop | Native fault / D3,D9 |
| Sleep/wake or cursor gap | Unknown until new bound evidence | Reducer + native / D3,D4 |
| Duplicate Attention / OS notification denied | Single transition notice; metadata only; in-app fallback | Component + native / D4 |
| Metadata limit / delete cancelled | Mutation rejected visibly; no eviction or deletion | Integration / D2,D8 |
| Output exceeds queue / unbound observation | Bounded output with gap; no foreign-run Attention | PTY + reducer / D3,D4 |
| Draft contains ESC/control/paste terminator | No injected keystrokes, approval or automatic Enter | Unit + native / D7 |
| Global + per-run hook compose | One history capture, or explicit unsupported/unknown | Host integration / D4 |

| Requirement | Owning planning issue |
|---|---|
| Public CLI observation intake, stdout/receipt/backward compatibility | D4 #2433 |
| Runtime lifetime, update deferral, opt-in auto-start, sleep/wake | D3 #2432; integrated D9 #2438 |
| Metadata bounds and export/delete | D2 #2431; existing mutation usecases D8 #2437 |
| Metadata-only notifications and Attention | D4 #2433 |
| Immutable text and safe agent Submit | D6 #2435, D7 #2436 |
| Signing, packaging and final native/compatibility evidence | D9 #2438 |

Planning issues contain ownership, dependencies, positive/negative acceptance and
rollback. Exact new frontend commands remain blocked on framework/source-layout
selection, not invented now. Existing `make docs/check`, staged selector and
final-head CI still apply; native gates cannot be replaced by documentation checks.

## Completion ledger

- [x] Scope and constraints recovered; real binary versions resolved.
- [x] Five-host documentation matrix; explicit unknown and evidence classes.
- [x] First Muse native echo probe and environment-binding failure recorded.
- [x] Concurrent Muse per-invocation correlation fixture and 16 synthetic reducer tests.
- [x] Proposed structure, contracts, threat model, test plan and rollback.
- [ ] Full five-host native interaction/approval/binding probes.
- [ ] Equivalent native UI measurements and all manual mandatory gates.
- [x] Independent Anthropic/Claude packet review completed; findings are triaged, not treated as approval.
- [ ] Final-delta independent review and maintainer architecture adoption decision.
- [x] D1–D10 planning child issues created with ownership, dependencies, positive/negative acceptance, validation and rollback; implementation remains blocked on D0 acceptance.
- [ ] ADR merged after verified acceptance and independent review. For this PR only, the maintainer explicitly authorized this main session to perform restoration, validation, review, fixes, merge and cleanup; the earlier human-UI-only delivery restriction is replaced, not the acceptance gates.

Do not close #2424 or claim certification while these gates remain open.

The first independent-review dispatch requested Claude Fable 5.1/xhigh with only
this paired ADR and sanitized evidence. The shared launcher stopped **before
provider execution** with `delegation_adapter_denied reason=author_unknown`:
the configured author model `gpt-6.1-sol` is absent from the local model registry.
That attempt produced no AI review; never substitute a different author ID,
weaken the gate or classify the empty attempt as approval. This was a legacy launcher metadata/version-coupling failure, not proof that a
model-version registration or pin is necessary. No dotfiles change is authorized
or made. A compliant direct read-only review may use the registered Claude engine
and its current CLI model selection without falsifying author identity or
weakening permissions. Main-session docs checks and synthetic tests are not
replacements for the required independent review.

The manual-input test fixture could not be opened through CUA (native app
lookup timed out), so no IME, clipboard or VoiceOver pass is recorded. No other
GUI automation mechanism or OS security workaround was used.

## Sources

Checked 2026-10-18; moving docs describe candidates, not installed-version proof.
Native evidence is summarized above rather than publishing private transcripts.

- D1: [Claude hooks](https://code.claude.com/docs/en/hooks)
- D2: [Codex hooks](https://learn.chatgpt.com/docs/hooks)
- D3: [Antigravity hooks](https://www.antigravity.google/docs/hooks)
- D4: [Muse hooks](https://dev.meta.ai/docs/muse-code/extending)
- D5: [Grok official source documentation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/10-hooks.md)
- D6: [cmux Feed](https://github.com/manaflow-ai/cmux/blob/main/docs/feed.md)
- [cmux agent hooks](https://github.com/manaflow-ai/cmux/blob/main/docs/agent-hooks.md)
- [Superset status](https://github.com/superset-sh/superset/blob/main/apps/docs/content/docs/agent-status.mdx)
- [Claude Squad classifier](https://github.com/smtg-ai/claude-squad/blob/main/session/tmux/tmux.go#L224-L244)
- [agent-deck status](https://github.com/asheshgoplani/agent-deck/blob/main/docs/status-detection.md)
- [Conductor harnesses](https://www.conductor.build/docs/reference/harnesses)
- [Electron security](https://www.electronjs.org/docs/latest/tutorial/security/)
- [Wails v2 options](https://v2.wails.io/docs/reference/options/)
