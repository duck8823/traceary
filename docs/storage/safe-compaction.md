# Safe store compaction

[日本語](safe-compaction.ja.md)

Garbage collection and archive deletion reclaim logical rows only. They no
longer run an in-place `VACUUM`. To reclaim filesystem space, stop all Traceary
processes (including older versions that do not participate in the lease) and
use the explicit workflow:

```sh
traceary store compact plan --db-path /path/to/traceary.db
traceary store compact apply RUN_ID --db-path /path/to/traceary.db
traceary store compact status RUN_ID --db-path /path/to/traceary.db
```

`plan` is non-destructive. `apply` uses `VACUUM INTO` beside the source,
fsyncs and verifies both SQLite files through the same compatibility policy,
then performs an atomic same-filesystem exchange. The original inode is kept as
a rollback artifact. A source with `-wal`, `-shm`, or `-journal` sidecars, a
still-resident legacy search index family, insufficient free space, changed
file identity, or an unsupported atomic-exchange platform fails closed.
The preflight requires 1.1 times the source store size in free space because
the candidate size is unknown until `VACUUM INTO` finishes, and the original
source-size rollback copy remains after success. See [store compact disk
cost](../operations/store-compact-disk-cost.md) for the operator guidance.

For `store compact`, `plan` holds the exclusive store lease for its entire
preflight. If it finds no
`-journal` and no non-zero `-wal`, it removes both regular `-wal` and `-shm`
sidecars when present, even when the `-shm` is non-zero, syncs the directory,
and checks again before opening the store. The shm file contains no database
content and is never fsynced; an empty WAL means all content comes from the
main database file. It never removes a non-zero WAL, any `-journal`, symlink,
FIFO, or other non-regular path. If one remains, stop all other Traceary
processes (including projection/status readers and older or non-cooperating
versions) and retry the same command; do not remove the sidecar manually. A
non-zero WAL or non-regular sidecar may contain live SQLite state.

`store compact apply` and `store compact resume` repeat that cleanup, because a reader can create a sidecar
after `plan` returns. Cleanup can only run once the exclusive lease is held, and
every live cooperating connection holds the shared form of the same lease, so a
sidecar is never removed while any of them has the store open — the lease
acquisition waits instead. The final pre-exchange check stays strict: by then
cleanup has already happened, so a sidecar appearing there means an opener
arrived mid-run and the run aborts.

This sidecar recovery is specific to `store compact`. The prepared-upgrade
path used by `store payload-rehearsal` does not hold the exclusive lease across
its preflight, so it refuses any SQLite sidecar rather than recovering it.

The legacy search index check runs before the source digest, so it fails in
seconds rather than after hashing a multi-GiB store. Compacting first would
copy the dead index into the new file and bake it in, so run
`traceary store search-retire` first — see
[`search-retirement.md`](../operations/search-retirement.md).

The plan reports `lease_capability`. On Darwin and Linux, every normal physical
SQLite connection holds a shared advisory lock on the stable adjacent
`<database>.traceary.lock` file. `apply`, `resume`, and `rollback` hold the
exclusive form from before journal/orientation inspection through completion,
including across the database inode exchange. Acquisition honors cancellation
and process termination releases the OS lock. The lock file remains on disk by
design. Existing database and parent-directory symlinks resolve to the same
lease namespace; hard-linked database files are rejected because aliases cannot
be fenced safely. Lease acquisition is required before `plan` can run, so
unsupported platforms fail at acquisition instead of producing a plan. A
persisted run therefore always has `lease_capability: true`; this field records
the completed plan's lease precondition rather than a later capability probe.
Operators must still stop older or non-cooperating processes.
The filesystem safety model is cooperative: every participating live opener
uses the adjacent lease, and every destructive boundary rejects hard-linked
source, candidate, or rollback files. Privileged or non-cooperating processes
that mutate directory entries remain outside this advisory-lock boundary.

After interruption, use `resume`; it derives file orientation from identities.
Use `rollback` to atomically restore the retained original. Never delete the
external `.traceary-compaction` journal or rollback artifact until the run has
been inspected and accepted.

## Preview-first maintenance sequence

1. Copy or back up the live store and stop older/non-cooperating processes.
2. Run `traceary store compact plan --db-path /path/to/traceary.db`. Review
   payload/projection allocation, reclaimable pages, filesystem headroom, and
   measured diagnostic latency; file size alone is not an apply decision.
3. Complete compatibility preflight and scrub on the reviewed copy. Never run
   an in-place `VACUUM`.
4. Run `compact apply RUN_ID` only after the plan passes. The safe engine builds
   and scrubs a candidate, atomically swaps it, and retains the original inode.
5. Verify normal reads before deleting rollback artifacts. Use `compact
   rollback RUN_ID` if verification fails.
6. After interruption, use `compact status RUN_ID` and `compact resume RUN_ID`;
   never manually rename candidate or rollback files.

Activation of the v0.35 payload/search policy is deliberately deferred. A
successful v0.34 compaction proves storage mechanics, not permission to enable
the next policy.
