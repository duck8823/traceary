# Explicit managed PTY launch

[日本語](managed-launch.ja.md)

This is the launch slice of [R0](../adr/2026-11-03-headless-runtime-r0.md).
The daemon owns actual child handles and PTY masters. A client connection is not
that ownership. Core Session records remain history grouping, not process state.
No wrapper, PATH shim, shell command, credential copy, model API, permission
bypass or host configuration rewrite is introduced.

## Register explicit operator policy

All `runtime` commands print JSON. Use the same absolute private `--runtime-dir`
as `daemon serve`. Registration explicitly creates that root; registry/profile
reads open an existing root and never create a missing directory or start agents.
The namespace is composed before core DB/config bootstrap, including with HOME
unset when an explicit root is supplied.

The following IDs, paths and version are **unverified placeholders**, not commands
with verified targets. Replace them with your own explicit operator selections.

```sh
traceary runtime project register --runtime-dir /absolute/private/runtime --path /absolute/project
traceary runtime worktree register --runtime-dir /absolute/private/runtime --project p_ID --path /absolute/worktree --db-path /absolute/existing/core.db
traceary runtime profile register --runtime-dir /absolute/private/runtime --id codex-local --host codex --executable /absolute/real/codex --expected-version 'EXACT NON-SENSITIVE VERSION'
traceary runtime registry list --runtime-dir /absolute/private/runtime
traceary runtime profile list --runtime-dir /absolute/private/runtime
```

`--db-path` here identifies an **existing** worktree store; its contents are never
opened, migrated or initialized by these commands. Git common-dir membership,
canonical worktree/store identity and profile selection are checked again before
spawn. Changing selection cannot rebind an existing run.

Profiles accept known host identifiers (`codex`, `claude`, `antigravity`, `grok`),
but registration is **not native interaction/recovery certification**. It pins an
explicit real executable's canonical path, physical/content witness and exact
non-sensitive version. The only probe is fixed `--version` (no stdin/PTY,
2-second probe timeout, 4 KiB output cap); invalid static policy is rejected before
path inspection. Trusted current-UID/root ownership, safe directory ancestors,
regular executable mode and witness are verified before running the probe.
Unknown or changed versions fail closed without echoing probe output.

Interactive launch argv is currently fixed empty. Management clients cannot
supply executable, argv, environment, cwd or store overrides. Host-specific launch
policy changes require their version-bound adapter work; do not insert a wrapper.
Authentication, subscription/billing, model defaults and approval decisions remain
with the real host CLI. Inherited environment stays in memory only; ambient Git
routing and Traceary store/runtime hints are replaced by the pinned selection.
No HOME or global host configuration is replaced.

## Launch and read receipts

Start `traceary daemon serve --runtime-dir /absolute/private/runtime` explicitly.
Read the current epoch with `traceary daemon status --runtime-dir ... --json`.
Then use registered IDs and your own unique explicit request ID:

```sh
traceary runtime launch --runtime-dir /absolute/private/runtime --epoch CURRENT_EPOCH --worktree w_ID --profile codex-local --request-id operator-request-1
traceary runtime list --runtime-dir /absolute/private/runtime --epoch CURRENT_EPOCH
```

`launch` sends one typed management request; it is not optional hook delivery.
Its total client budget is eight seconds, separately from the optional 250 ms
observation budget. Server handshake/frame admission remains bounded; write
deadlines are renewed after a bounded management operation. Possible post-send
transport loss returns **unknown**, echoing the original public epoch/request ID;
never interpret it as “no process started” or automatically send a new request.
Explicitly looking up the **same** request/target/profile in the same epoch
returns its original receipt without spawning another child. A different target
under that key is refused. Old epochs are stale, never adopted after restart.

Run, generation, epoch, immutable target/profile, diagnostic PID, process state,
exit code and publication uncertainty are metadata only. A PID is not a control
handle; process `live` does not mean AI working/waiting/approval or input authority.
List pages contain at most two runs and are trimmed to the actual escaped 64 KiB
wire bound. Continue using `next` as `--cursor` and the same `--revision`; changed
revisions are refused instead of mixing snapshots. Oversize descriptors are
refused before spawn, not truncated after a child is created.

Admission is 256 active/pending ownership reservations and 1,024 request receipts
per epoch. Existing receipts remain available at capacity. No uncertain entry is
evicted to admit new work. Durable journal capacity is also bounded; an owner-
private reserved intent must commit before spawn. Pre-commit cancellation may
abandon an unstarted reservation; after that boundary cancellation does not erase
the request or kill a child. Journal failure after spawn retains child ownership
and reports unknown, never blind replay.

## Ownership, cancellation and durability

PTYs are controlling terminals in new child sessions. Output is continuously
drained even without a client. This slice discards output; bounded ring/cursors
and reconnect are the separate #2462 requirement, not claimed complete here.
The actual owned `exec.Cmd.Wait` reconciles exit exactly once; neither persisted
PIDs nor external CLI observations authorize attach, signal or adoption.

Interrupting foreground Serve disables **new** launch admission. If owned runs
remain, it enters `draining`, retains the owner lease and management endpoints,
and waits for actual owned exit and journal reconciliation. It does not close
live PTYs or silently terminate agents. Existing receipts and status/list remain
readable. A broken/uncertain child transfer conservatively retains its reservation.
Explicit target-bound stop (#2463) and planned two-step shutdown (#2464) are still
separate requirements; this is not a claim those workflows are implemented.

Profiles and reserved/live/exited journal records are versioned 0600 metadata
inside the pinned 0700 root, sharing the registry's OS lock and aggregate 128 MiB
committed metadata budget. Writes use bounded temporary files, sync, rename and
parent-directory sync. Raw environment, prompts and PTY bytes are not serialized.
Lock first creation uses no-follow existing-open/exclusive-create, with bounded
EEXIST reconciliation and no permission repair or pathname fallback. Same-UID
hostile software, different-UID/ACL and real host behavior are not certified by
same-owner synthetic fixtures.

Daemon crash can close PTYs and end agents. Safe explicit resumption of the same
native conversation is the permanent #2478 product requirement; this launch
journal alone is not a recovery record and never authorizes auto-resume/replay.
UI adoption/distribution and Muse native work remain out of scope/deferred.

Rollback disables new launch; preserve active ownership and confirmed metadata.
Never blanket-kill agents, delete user stores/worktrees, or unlock an uncertain
owner merely to make shutdown appear successful.
