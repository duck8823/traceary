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
a rollback artifact. A source with `-wal`, `-shm`, or `-journal` sidecars, an
active search-maintenance transition, insufficient free space, changed file
identity, or an unsupported atomic-exchange platform fails closed.

The plan reports `lease_capability`. On Darwin and Linux, every normal physical
SQLite connection holds a shared advisory lock on the stable adjacent
`<database>.traceary.lock` file. `apply`, `resume`, and `rollback` hold the
exclusive form from before journal/orientation inspection through completion,
including across the database inode exchange. Acquisition honors cancellation
and process termination releases the OS lock. The lock file remains on disk by
design. Existing database and parent-directory symlinks resolve to the same
lease namespace; hard-linked database files are rejected because aliases cannot
be fenced safely. Unsupported platforms and failed capability probes report `false` and
fail closed. Operators must still stop older or non-cooperating processes.
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
