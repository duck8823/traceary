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
| Claude Code 2.1.304 (N1) | Prompt/tool hooks (D1); native unknown | Notification subtype (D1), delayed; native unknown | `permission_prompt` (D1); request alone insufficient; native unknown | Stop (D1); native unknown | Child wait only; hook alone unknown |
| Codex 0.163.0 (N1,N3,N4) | Prompt captured in samples (N3,N4); tool hooks/current working state unknown (D2) | Dedicated proof unknown (D2) | PermissionRequest is telemetry, human visibility unknown (D2,D6) | Stop observed in three headless sample turns (N3,N4); not input readiness or process-exit evidence | Supervised native child exits 0 (N3,N4); hook alone unknown |
| Antigravity CLI 1.4.5 (N1) | Invocation/tool hooks (D3); native unknown | `fullyIdle` alone does not prove input acceptance (D3) | Unknown (D3) | Stop plus `fullyIdle` (D3); native unknown | Child wait only; hook alone unknown |
| Muse Code 1.4.10-R6427.1 (N1) | PreLLMCall observed in echo only (F1,D4) | Unknown; Stop does not prove it (F1,D4) | Notification documented (D4); native unknown | Stop observed in echo only; Meta-provider behavior unknown (F1) | SessionEnd not observed in F1; child wait only |
| Grok Build 1.0.62 (1474f9cbdd91) (N1) | Prompt/tool hooks (D5); native unknown | `idle_prompt` documented on main, installed-version behavior unknown (D5) | `permission_prompt` documented on main, native unknown (D5) | Stop documented (D5); native unknown | Child wait only; hook alone unknown |

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
direct native variants delivered all 188,743,680 bytes with
200 resizes and source EOF in 60,281.4 ms (Electron) / 60,269 ms (Wails).
The old zero-drop field was a constant, not a measured parser/display drop counter.
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

## Evidence refresh from 2026-10-22 and offline diagnosis on 2026-10-23

This is a Proposed evidence update, not D0 acceptance. The [sanitized refresh evidence](evidence/desktop-d0-2026-10-23.json) distinguishes historical failures, new source cohorts, native observations and unexecuted gates. Neither runtime is adopted. The earlier CUA timeouts remain historical failures; later successful native observations supersede only the blanket claim that no native input was ever delivered.

### Host/version and binding observations

N1 records 2026-10-22 binary metadata: Codex 0.163.0, Claude Code 2.1.304, Antigravity 1.4.5, Muse 1.4.10-R6427.1 and Grok 1.0.62(1474f9cbdd91). Claude's direct native binary version was separately rechecked as 2.1.304 for this refresh; a routed banner is not used as native-launch proof. N0 denotes the earlier 2026-10-18 metadata; N1 is the later banner record in the refresh JSON. These are host-version observations, not model-version pins or certification of untested states.

N2 denotes the native binding probe and reproducible fixed protocol in the refresh JSON. Two fresh, overlapping Antigravity 1.4.5 headless native invocations from one empty cwd routed to distinct throwaway Traceary databases. Each database had exactly its own native grouping and three own metadata rows; supervised processes exited 0. This establishes scoped native correlation and explicit store routing, not input readiness, approval visibility, ordering/generation, descriptor security or authority. One exact-response punctuation mismatch and the initial verifier's inherited-workspace failure are retained. Muse's two-run echo/literal-hook correlation remains echo-only; arbitrary environment binding was not preserved. Claude funding failure produced no native interaction proof. Codex hook-hash trust and Grok unified project trust remain intact; no broad trust/config change or Grok model request was used to manufacture evidence. All unproven interaction cells remain unknown.

### Fixed packaged real-PTY batch, not a controlled foreground ranking

The two unchanged, signed v4 fixture bundles completed one finite CLI-driven batch: five fresh-process startup observations per runtime, then one nominal load and one minimized/background trial each. Zero rebuilds, version increments and Computer Use calls occurred within that batch. Original three PTYs × 1 MiB/s × 60 s, 2 MiB/terminal bounds, 200 terminal/native size requests, 1000-line scrollback and 100k-input/5k-preview diff workload were retained. Source, generated assets, protocol and regular bundle-file hashes matched before/after. Electron's own accessibility rendering was explicitly enabled; its cost belongs to this source cohort. Native UI actions performed for separate input trials are not counted as part of this automatic batch.

The RSS column is the maximum of sampled per-app member RSS sums (`sampledPeakRSSBytes`), not an OS high-water mark.

| Runtime / requested mode | Generated bytes delivered | Terminal resize receipts | Immediate native size comparisons matching | Sampled idle CPU, one logical CPU | Peak of sampled summed RSS bytes |
|---|---:|---:|---:|---:|---:|
| electron / load | 188743680 | 200/200 | 200/200 | 0.703% | 566591488 |
| wails / load | 188743680 | 200/200 | 0/200 | 0.420% | 557121536 |
| electron / background | 188743680 | 200/200 | 200/200 | 0.519% | 564887552 |
| wails / background | 188743680 | 200/200 | 0/200 | 0.293% | 560889856 |

All four real-PTY workload trials (two nominal load and two background) had 3 EOFs, pending 0 and zero fixed-payload byte mismatches. This is byte conservation plus received bridge-payload comparison, not a measured terminal parser/display drop counter (`droppedBytes=null`). Both nominal load trials have negative sampled foreground evidence; non-minimized is not foreground. Their observations do not certify a controlled foreground/background framework comparison.

Packaged startup descriptive n5 nearest-rank p95 was 1855.65 ms for Electron and 1020.13 ms for Wails. No cold-cache or confidence/ranking claim follows. Equivalent fixture bundle regular-file sizes were 308336437 and 14593507 bytes. The first-post-ready idle sample through the next bracketed idle sample≥60s used calibrated cumulative CPU ns, not ps EWMA. Stable sampled distinct-coalition membership is not proof that no transient helper existed; sampled RSS can count shared pages more than once and is not an all-time peak. Fixed threshold comparisons are descriptive, not broad CPU/RSS/native acceptance. Older Electron startup 3150.77 ms and background terminal resize 8/65 failures remain unchanged in their original cohorts.

### Native input observations have separate, limited scope

Separate real native textarea actions observed Japanese commit/cancel and Romaji-to-Kanji conversion in both runtimes. Electron preserved the known decomposed Unicode paste. Wails failed exact preservation twice (UTF-16 length 11 vs expected 12; observed composed U+00E9 vs source U+0065 U+0301); the responsible CUA/OS/WebKit component is unproven. No normalization changes the expected value. Japanese was already active; no source/shortcut was changed or switched, exact active-source ID was not captured and the ASCII baseline did not pass.

A subsequent Electron xterm fixture retained four trusted native-ingress→samePTY0→matched echo→xterm-write→two-animation-frame software samples (CR, Japanese commit, Kanji and known Unicode paste). The native broker ledger corroborated all four exact echoes. These are a small unloaded diagnostic, not physical key-to-display latency, full-load interaction p95 or all IME/Monaco/host input acceptance. The initial Electron diagnostic had zero renderer native samples but one backend CR ledger entry; no renderer/native success is inferred from it alone. The Wails xterm attempt received no native input after a stale-target tool guard and ended at its existing execution deadline. No Wails xterm input pass is claimed. Native menu Quit and own-process absence were observed separately; close-detach/reopen and daemon/PTY lifetime remain untested.

### Wails immediate native-size comparison is not a completion acknowledgement

Verified installed Wails v2.16.0 source queues SetSize asynchronously on the main queue; GetSize does not wait for that queued setter. Our controller compares one immediate read, then report construction queries the size independently. All 200 first comparisons are false in each Wails trial, while the later report already matches 4 requested frames in load and 2 in background; both final queried frames are 1300 × 904. The flags are failed immediate comparisons, not proof of zero actual resizes. They do not pass the native200 gate either. `go mod verify` and eight offline source/receipt checks passed. The installed module origin records commit 457bf210d5eb976ff3726c8629f7725575163307; pinned web source retrieval failed, so no successful external retrieval is claimed. A basename-colliding source snapshot was caught by hashes and corrected with the failed snapshot retained; upstream code was not changed.

A future measurement must keep the same 200-command/150 ms cadence and original workload, and associate bounded request/generation identities with main/UI-thread application acknowledgements. Actual bounds, comparison and acknowledgement must come from the same capture. Missing/late/duplicate/reversed acknowledgements are failures, not reasons to slow the storm, shift indices, apply a height offset or poll until a passing result. This is a proposed fixture-measurement correction, not a product API or an implemented/native-accepted guarantee.

### Remaining delivery and acceptance gates

Full five-host interaction/approval/authority, loaded native input, Wails xterm input, Monaco IME, VoiceOver, sleep/wake/crash, close/reopen and multiple-monitor gates remain incomplete. Only one built-in display is available; the maintainer cannot supply an external display. That is unexecuted, not passed or waived. Prior design PR2429 was merged under an explicitly PR-specific Proposed-document exception; that historical permission did not authorize another incomplete-closing PR. On 2026-10-23, the maintainer explicitly extended permission to this evidence refresh (#2441): its PR may merge and close only that bounded sub-issue while #2424 remains open. The full Desktop goal remains paused; this permission does not accept D0 or adopt a runtime. Original full D0 scope, independent review and human-gated adoption remain required. This refresh changes no production code, schema, credential/config/trust setting or distribution. Temporary native apps were removed after evidence preservation; identical-binary evidence must not be claimed for a future rebuild.

## 2026-10-23 reproducible non-Muse follow-up

The maintainer explicitly authorized a separate evidence-only integration for #2443 while #2424 remains open. The [reproducible probe sources and commands](evidence/desktop-d0-probes-2026-10-23/README.md) and [sanitized metadata](evidence/desktop-d0-probes-2026-10-23.json) preserve two limited results without changing this Proposed contract or adopting a runtime. Muse remains deferred and is not invoked.

- The synthetic resize receipt ledger passes 31 tests, including 200 interleaved requests at 150 ms cadence. Independent static review identified a reused-capture false-success path; the main session reproduced/fixed it and a final review reported no remaining findings. Capture ID/pair reuse now invalidates both request classifications. No native UI completion or physical presentation is certified; the 500 ms fixture deadline is not an adopted performance threshold.
- The macOS Unix-socket feasibility probe passes 29 tests, including a separate same-UID process and actual libc getpeereid, 0700/0600 mode bits, bounded/fragmented frames, a total 250 ms exchange deadline and an observation-only fixture. Different-UID clients, ACLs, same-UID adversarial races and full sender authorization are not certified. It is not a product daemon, adopted public protocol or proof of history/lease/PTY integration.

The sources are deliberately outside production packages. Their tests require no Desktop app build/launch, version increment, GUI automation, login or privilege change; owned local socket fixtures are cleaned. These results do not pass the remaining native/manual/hardware gates or the final High-risk architecture/adoption gate. Original native failures and unknown states remain unchanged.

## 2026-10-23 native Codex sample follow-up

N3/N4 in the [sanitized Codex evidence](evidence/desktop-d0-codex-native-2026-10-23.json) replace only the blanket native-unknown statements for the sampled prompt/Stop and history correlation. One single turn and two concurrent fresh Codex CLI0.163.0 ephemeral turns completed normally; each requested throwaway DB contained only its corresponding native thread as one session and three configured-hook events. The two concurrent process handles were observed live, shared one empty cwd, returned distinct expected markers and showed no opposite-run marker in the inspected records. These are three bounded samples, not a universal concurrency or live-state guarantee.

On-request approvals, read-only sandbox and existing Guardian/hook trust were retained; model shell, multi-agent and dependency-auto-install capabilities were disabled. The initial approval-never proposal was rejected before execution; the separately permitted on-request alternative did not bypass that boundary. The parent supplied only synthetic prompts and inspected owned-store metadata/marker booleans, not global history or raw event bodies. Native CLI system/config context and full OS isolation were not independently audited. Local source/protocol/archive hashes are retained; the tracked metadata alone is not a complete independently replayable fixture.

A session grouping's ended_at remained absent despite supervised child exit. Stop, committed history and native process exit are separate evidence; none grants input/approval authority. Arbitrary environment propagation, a generation witness, prechosen-session/resume behavior, descriptor security, global/per-run hook composition, all UI/manual/hardware gates and final adoption remain unknown. No earlier failure was rewritten into a pass.

The maintainer explicitly authorized this additional evidence-only integration while #2424 remains open, D0 stays Proposed/incomplete, neither runtime is adopted and the full goal stays paused. No further native qualification, Muse invocation, Desktop build/launch, version increment or Computer Use is performed for this delivery.

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
| Codex | N3/N4: fresh native thread and explicit per-invocation DB-route/cwd association observed (0.163.0); arbitrary environment, prechosen session/generation and descriptor authority unknown | Native global/per-run hook composition unknown |
| Antigravity | N2: native fresh-session → explicit per-invocation store correlation observed (1.4.5); arbitrary environment, prechosen session/generation and descriptor authority unknown | Native global/per-run hook composition unknown |
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
- [x] Final-delta Anthropic/Claude review of `5b0b41d6` on 2026-10-18; its lexical/ledger conditions are verified separately by the main session.
- [ ] Maintainer architecture/framework adoption decision.
- [ ] Full D0 acceptance and Status change to Accepted.
- [x] D1–D10 planning child issues created with ownership, dependencies, positive/negative acceptance, validation and rollback; implementation remains blocked on D0 acceptance.
Proposed-document delivery: on 2026-10-18 the maintainer explicitly authorized
this main session to restore, validate, arrange independent review, fix, merge
and clean up this PR, replacing the earlier human-UI-only delivery restriction.
The maintainer also explicitly allowed a PR-specific closing-rule exception for
Proposed integration with #2424/#2414 open. The bounded documentation checkpoint
is tracked separately as #2440; no native acceptance or architecture adoption is
implied. Integration status is the actual PR #2429 history, not a D0-acceptance
checkbox in this document.

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

The earlier manual-input fixture lookup timed out. Later limited native input
observations are recorded in the dated refresh above; they do not pass the full
manual/VoiceOver gate. No alternate GUI driver or OS-security workaround was used.

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
