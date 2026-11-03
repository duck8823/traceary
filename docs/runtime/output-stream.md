# Bounded local PTY output

[日本語](./output-stream.ja.md)

The daemon retains raw PTY bytes **in memory only**, independently of a reader's lifetime. Closing, crashing or cancelling an attach client detaches that reader; it never restarts or terminates the owned child. No daemon-crash PTY restoration is promised. Explicit safe native-session recovery is a separate contract.

```sh
traceary runtime attach --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --cursor 0
```

All five immutable selection fields must match an actual child owned by the current daemon epoch. A saved PID, old epoch, pre-spawn ticket or changed generation grants no attach authority. Current owned exited and publication-uncertain runs remain readable. Attach never launches a host or initializes the core database.

## Frames and counters

The management-only, one-way stream emits `snapshot`, `data`, `gap`, and `end` JSON lines. Bytes in `data` are base64; offsets and resize sequences are **canonical decimal strings**, not JavaScript numbers. Offsets count bytes, not Unicode characters, and chunks may split UTF-8 or terminal escape sequences. The CLI never renders escapes or performs OSC clipboard/link actions. Do not decode and render untrusted content without a separate terminal security policy.

Reconnect explicitly with the same selection and the last processed data `end` or gap `to` offset. A snapshot reports retained `base` and captured `head`, capture completion, and the last owner-applied resize acknowledgment. It is not a delivery receipt or process-exit notification. A cursor ahead of `head` is rejected. A cursor behind `base` first receives an exact `evicted` gap; it is not silently clamped. Eviction can also occur while replaying old scrollback.

An `end` ends capture, not necessarily the process. EOF/EIO can close capture while the child lives. After actual owned Wait, the single PTY reader drains kernel tail bytes for at most 250 ms or 2 MiB; a descendant-held slave or exhausted tail budget produces an explicit `tail_unknown` gap with `delivery_unknown: true`. Unknown tails may have a zero-width known range: their true missing length is unknown.

## Fixed bounds and slow readers

- Retention: at most 2 MiB per owned run, allocated lazily; native/read chunks at most 32 KiB.
- At most four attached readers per run and 32 management connections globally.
- Each reader has at most 256 KiB of pending wire budget, including base64 and frame overhead. Old scrollback is replayed lazily from the shared ring, not duplicated into queues. Only new live backlog is charged; an empty backlog costs zero.
- A slow reader is detached with an explicit `slow_consumer` unknown-delivery gap when its budget is exceeded. Socket writes have a bounded deadline; an unwritable connection is closed, so its peer must treat delivery as uncertain and explicitly reconnect from its own last processed cursor. The producer never waits for a reader.
- At most 1,024 launch records per epoch imply a worst-case 2 GiB volatile terminal-byte ceiling, plus bounded metadata. This is a derived bound, not a memory-performance certification. Exited-run compaction is separate work.

Idle stream reads have no inherited RPC timeout. Admission is bounded to eight seconds; each frame write has the configured I/O timeout. EOF or any extra client byte after admission closes only that subscription. Client cancellation/Close releases the stream and connection slot without signalling the child. There is no automatic retry, launch or resume.

Resize evidence starts at sequence `0`, 24 rows × 80 columns only after the owner's successful PTY setup. It advances only after owner-applied acknowledgment; exact duplicates are idempotent, conflicts/stale acknowledgments are refused. Native child self-resizes and text cannot forge this evidence. It does not assert current native geometry or historical byte layout. Public resize/input/stop operations are separate work.

## Validation and rollback

Synthetic native PTY tests cover exact byte counts, Unicode/TUI/OSC bytes, resize storms, exit tails, slow-reader eviction, cancellation and same-generation reconnect. These do not certify a real AI host, native IME, VoiceOver or a UI framework. No credentials, wrappers or alternate billing paths are involved.

Rollback removes new attach admission and detaches readers while keeping child ownership and safe launch metadata. Volatile history is not restored across daemon restart; missing bytes must never be presented as delivered.
