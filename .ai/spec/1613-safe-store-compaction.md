# Safe store compaction — design checkpoint

## Safety contract

Compaction is a separate maintenance workflow. Garbage collection and archive
deletion never run `VACUUM`. A compaction run is explicit, resumable and
recorded in a durable, size-capped journal outside the database being replaced.
Normal readers/writers acquire a shared store lease; the compactor acquires an
exclusive lease and opens SQLite directly, without normal initialization or WAL
side effects. Older/non-cooperating processes cannot be fenced, so operators
must stop them before apply.

The source remains byte-for-byte unchanged until a candidate has been copied,
synced and verified. Source and candidate are opened read-only by the same
`VerifyStoreCompatibility` implementation. Verification includes schema digest,
`integrity_check`, zero foreign-key violations, an order-independent logical
multiset over every ordinary table plus `sqlite_sequence`, and payload-codec
scrubbing. Implicit rowids are not part of logical identity.

Replacement is allowed only by a same-directory, same-filesystem atomic
exchange (`renameatx_np(RENAME_SWAP)` on Darwin or
`renameat2(RENAME_EXCHANGE)` on Linux). Unsupported platforms fail closed.
Paths are no-follow checked and fenced by device, inode, size and mtime. SQLite
sidecars must be absent. Files and containing directory are fsynced. The old
inode remains at the candidate/original path for rollback; publishing rollback
artifacts is no-replace.

## State machine

`planned -> copy_intent -> copy_complete -> candidate_sync_intent ->
candidate_synced -> scrub_in_progress -> candidate_verified -> swap_intent ->
swapped -> rollback_publish_intent -> rollback_ready -> committed`

Rollback uses `rollback_swap_intent -> rollback_swapped -> rolled_back`.
Resume determines orientation from fenced identities rather than trusting the
last journal record. The journal rejects invalid transitions, oversized input,
truncation and run/store mismatches.

## Operational constraints

Plans reject non-static search-maintenance state, cross-filesystem paths,
sidecars and insufficient free space using overflow-safe arithmetic. Apply,
resume and rollback require the exclusive lease. A direct SQLite connection
issues `VACUUM INTO` with a safely quoted candidate path. The dedicated CLI
exposes `plan`, `apply`, `resume`, `status`, and `rollback`; it does not reuse
payload rehearsal or backup/restore helpers. Actual production apply is never
implicit. Windows and other systems without an equivalent atomic exchange are
unsupported and fail before copying.

The large-shape harness is opt-in and models a 21.4 GiB source without requiring
that allocation in the normal test suite.
