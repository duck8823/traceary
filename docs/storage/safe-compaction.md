# Safe store compaction

[日本語](safe-compaction.ja.md)

`traceary store compact` rewrites the store file. It no longer runs an in-place
`VACUUM`. To reclaim filesystem space, stop all Traceary processes (including
older versions that do not participate in the lease) and run:

```sh
traceary store compact --db-path /path/to/traceary.db
```

The command holds one exclusive lease for the whole rewrite. `VACUUM INTO`
builds a filtered candidate beside the source. Use `traceary store compact
rollback RUN_ID` to restore the retained original inode. `plan` / `apply` /
`resume` / `status` are gone.

The rewrite uses `VACUUM INTO` beside the source,
fsyncs and verifies both SQLite files through the same compatibility policy,
then performs an atomic same-filesystem exchange. The original inode is kept as
a rollback artifact. A source with `-wal`, `-shm`, or `-journal` sidecars,
insufficient free space, changed file identity, or an unsupported
atomic-exchange platform fails closed. A still-resident legacy search index
family is dropped on the work copy.
The preflight requires 1.1 times the source store size in free space because
the candidate size is unknown until `VACUUM INTO` finishes, and the original
source-size rollback copy remains after success. See [store compact disk
cost](../operations/store-compact-disk-cost.md) for the operator guidance.

For `store compact`, the exclusive store lease covers the entire rewrite. If it finds no
`-journal` and no non-zero `-wal`, it removes both regular `-wal` and `-shm`
sidecars when present, even when the `-shm` is non-zero, syncs the directory,
and checks again before opening the store. The shm file contains no database
content and is never fsynced; an empty WAL means all content comes from the
main database file. It never removes a non-zero WAL, any `-journal`, symlink,
FIFO, or other non-regular path. If one remains, stop all other Traceary
processes (including projection/status readers and older or non-cooperating
versions) and retry the same command; do not remove the sidecar manually. A
non-zero WAL or non-regular sidecar may contain live SQLite state.

The same cleanup runs before the candidate is built, because a reader can create a sidecar
before the exclusive lease is held. Cleanup can only run once the exclusive lease is held, and
every live cooperating connection holds the shared form of the same lease, so a
sidecar is never removed while any of them has the store open — the lease
acquisition waits instead. The final pre-exchange check stays strict: by then
cleanup has already happened, so a sidecar appearing there means an opener
arrived mid-run and the run aborts.

This sidecar recovery is specific to `store compact`.

`hook memory-extract-worker` holds a shared store lease for each Extract job.
Before starting a job it checks an internal compact-pending marker next to the
store lease file. If the marker is present it finishes nothing new: the current
job (if already running) completes, then the worker exits and leaves remaining
jobs on the spool. Compact writes that marker before waiting for the exclusive
lease and removes it when the lease is released. Exclusive-timeout errors name
the lock holder pid and command when `lsof` can see them. Do not kill extract
workers by hand; drain happens by this backoff, or by `doctor --fix` if you
need the spool empty for another reason.


The retired search index family is dropped on the work copy. Compact no longer
refuses a source that still carries it. See
[`search-retirement.md`](../operations/search-retirement.md).

Compact preserves `event_content_dedupe_archive` (the content-event dedupe
quarantine audit trail) within a 90-day retention window; rows older than that
are discarded at compact.

On Darwin and Linux, every normal physical
SQLite connection holds a shared advisory lock on the stable adjacent
`<database>.traceary.lock` file. Compact holds the
exclusive form from before journal/orientation inspection through completion,
including across the database inode exchange. Acquisition honors cancellation
and process termination releases the OS lock. The lock file remains on disk by
design. Existing database and parent-directory symlinks resolve to the same
lease namespace; hard-linked database files are rejected because aliases cannot
be fenced safely. Lease acquisition is required before compact can run, so
unsupported platforms fail at acquisition. Operators must still stop older or
non-cooperating processes.
The filesystem safety model is cooperative: every participating live opener
uses the adjacent lease, and every destructive boundary rejects hard-linked
source, candidate, or rollback files. Privileged or non-cooperating processes
that mutate directory entries remain outside this advisory-lock boundary.

After interruption, rerun `traceary store compact`; it resumes an in-flight
journal when one exists. Use `rollback RUN_ID` to atomically restore the
retained original. Never delete the external `.traceary-compaction` journal or
rollback artifact until the run has been inspected and accepted.

## When the rollback copy is released

`VerifyPair` at the end of apply is a real check (compatibility, filtered or
logical digest, attestation). It is not proof that the compacted store is
correct in use. Traceary therefore **does not** delete `<db>.rollback-<run>`
on commit or on the next successful open, and it does not add a release
subcommand.

The operator releases the copy by deleting the path named in the compact
success JSON (`rollback_path`, with `rollback_retained: true`). Deleting it
gives up `traceary store compact rollback RUN_ID` for that run.
`traceary doctor` reports a leftover sibling as `compact-rollback-copy`.

## Maintenance sequence

1. Copy or back up the live store and stop older/non-cooperating processes.
2. Fold the oldest sessions you are willing to reclaim (`traceary-session-refine`).
   Compact after a partial fold reclaims what those sessions authorize.
3. Run `traceary store compact --db-path /path/to/traceary.db`. Never run an
   in-place `VACUUM`. `--force` writes mechanical summaries for unrefined
   discardable-age sessions and states the loss of agent reasoning. Compact
   also clears leftover `command_executed` bodies that already have a
   `command_audits` row and reports `released_command_body_bytes` as the
   stored blob sum. File size after the rewrite is `bytes_after`.
4. If search is drifted, run `traceary store compact --projection-rebuild`
   (start or replace), then run the same command again to resume until
   complete. Verify normal reads before deleting rollback artifacts. Use
   `compact rollback RUN_ID` if verification fails.
5. After interruption, rerun `store compact`; never manually rename candidate
   or rollback files.
