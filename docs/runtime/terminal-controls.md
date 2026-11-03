# Explicit owned-terminal controls

[日本語](./terminal-controls.ja.md)

The private local management socket offers named `input`, `resize`, `stop` and `receipt` operations. They require the exact current `(epoch, run, generation, worktree, profile)` selection and an actual runtime-owned child. Saved PIDs, public IDs, observation telemetry and old epochs never grant control. Both peers verify OS UID; this is not isolation from malicious software already running as the same owner.

## CLI

Use the selection returned by `runtime launch` / `runtime list`:

```sh
traceary runtime input --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --manual-terminal < /path/to/operator-selected-input

traceary runtime resize --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --sequence 1 --rows 31 --cols 101

traceary runtime stop --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --acknowledge-work-loss

traceary runtime receipt --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id>
```

Input reads **1–16,384 bytes from stdin only**. Do not put sensitive input in arguments or shell history. The CLI does not append Enter/newlines, interpret bytes, paste review batches or answer native approvals automatically. `--manual-terminal` classifies explicit operator intent; it is not a cryptographic assertion that the caller is human. Observation waiting/approval events never create this permission. Authentication, subscription, model defaults and approval decisions remain host-owned.

Resize requires the exact next canonical decimal sequence (initial owner acknowledgment is `0`, 24×80). Rows/columns are 1–65,535. Native ioctl succeeds before owner acknowledgment advances. An existing identical receipt is inspected without a second ioctl; a new request cannot reuse or skip a sequence. Child self-resizes/text and capture-end do not manufacture acknowledgment or process-exit evidence.

**Stop may lose work.** It sends fixed SIGTERM to the original owned launch process group, not an arbitrary/current foreground group. In-group descendants may be affected; escaped sessions/groups are not swept. Kernel controlling-terminal hangup when a leader exits can affect its foreground group. There is no automatic SIGKILL escalation, orphan signal, blanket kill or old-PID adoption. A delivered stop receipt means a kernel signal attempt, **not** process exit; actual owned Wait reports exit separately.

## Receipts, uncertainty and bounds

A request ID is a non-secret correlation reference (1–128 ASCII letters/digits, `-`, `_`). Reusing an identical intent/content/selection inspects the original receipt and never resends input, even after child exit, disk witness changes or admission exhaustion. Reusing an ID with different content/intent is rejected. An unknown result must not be automatically retried with either the same or a new ID; inspect it and make an explicit informed operator decision.

Safe JSON output includes selection, request ID/kind, canonical decimal `input_bytes` / `written_bytes`, resize evidence and `pending`, `delivered`, `unknown` or `rejected` outcome. A known written prefix does not prove native understanding or exactly-once terminal execution. A partial/interrupted write, post-send timeout/lost/malformed/contradictory acknowledgment or publication uncertainty is unknown. Zero known written bytes is not proof that nothing was delivered. Receipt lookup is read-only; an in-flight pending result is not a promise that durable intent has already committed.

New operations revalidate registered target/store, stored profile and pinned on-disk executable witness before reservation. Matching existing receipts bypass these new-effect checks. At most 32 coordinators run globally, with one operation per owned run; excess is busy, not an unbounded queue. The application retains ownership and joins admitted coordinators even if the caller cancels or a stop exits the child before receipt reconciliation. Effect/admission budget is eight seconds plus a separately bounded two-second metadata reconciliation phase; uninterruptible OS I/O is not falsely claimed to be forcibly cancellable.

Each run has at most 1,024 receipts; input+resize consume at most 1,008, reserving 16 lifecycle slots. Unknown evidence is not evicted. A definite precommit failure abandons only the uncommitted reservation; ambiguous intent commit produces unknown and no native effect. Per-epoch HMAC conflict fingerprints stay in memory only. Private versioned `receipts.json` contains metadata only, no raw bytes, content hashes, HMAC keys, environment or PIDs. It is capped at 64 MiB and shares the existing 128 MiB root budget. Reserved slots do not guarantee available disk. Old epochs remain historical metadata, never restored live authority.

## Native safety and certification limits

Darwin uses a non-reaping kqueue exit witness; Linux uses pidfd polling. Every native effect shares a mutex with a permanent controls-closed latch established **before** the sole actual Wait may reap; FD close follows pump completion. Kernel SIGCHLD disposition is queried read-only before spawn/control, rejecting ignored/NOCLDWAIT policies without rewriting process-global signal configuration. Unsupported signal ABIs (currently Linux MIPS) fail closed. Watcher failures close control admission while retaining actual child Wait. The pure-Go Darwin read-only syscall uses the verified old-action ABI and fails closed if unavailable; it is intentionally not a private runtime/linkname or CGo fallback.

These guarantees assume the standalone trusted daemon has the sole reaper and no foreign in-process signal-policy mutator; they do not promise hostile same-UID/debugger isolation. Synthetic PTY/socket/CLI tests are not real-host, native-session recovery, UI/IME or VoiceOver certification. Safe explicit same-native-session recovery is required separately; no automatic resume or input replay is introduced here.

Rollback disables mutating admission while preserving read-only list/attach, actual ownership and safe receipts. Never resolve an unknown delivery by resending it automatically.
