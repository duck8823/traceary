# Payload compression backfill (live store)

[日本語](payload-backfill.ja.md)

`traceary store payload-backfill` rewrites every existing `events.body` through
the versioned zstd codec already shipped in `payload_codec.go`. It is the
operator command that applies compression to retained history.

This is **not** `store payload-rehearsal`. Rehearsal writes only to a shadow
table on a *copy*, freezes inserts, and refuses to start once the live store
holds any non-identity payload. It predicts cost; it cannot perform the work.
Backfill mutates the live store in place, never freezes writers, and treats a
mixed identity/zstd corpus as a fully valid store at every batch boundary.

## When to run it

- After upgrading to a build that includes migration 053 (codec-aware body
  provenance triggers) and 054 (backfill bookkeeping).
- Before relying on the #1620 store-size gate numbers that assume compressed
  event bodies.
- On a maintenance window where you can rebuild the search projection and run
  `store compact` afterwards.

`command_audits` text columns are **out of scope** (tracked separately).

## Procedure

1. **Backup** the live store (for example `traceary store backup create`).
2. **Preview** eligible work without writing:

   ```sh
   traceary store payload-backfill preview
   ```

3. **Run** the rewrite (resumable; bounded batches):

   ```sh
   traceary store payload-backfill run
   # optional pacing:
   traceary store payload-backfill run --batch-rows 256 --stop-after-batches 100
   ```

4. If interrupted or paused, **resume** with the same binary recipe:

   ```sh
   traceary store payload-backfill resume
   ```

5. Check progress:

   ```sh
   traceary store payload-backfill status
   ```

6. **Rebuild the search projection.** The backfill updates `events.body`, so the
   projection invalidation triggers fire and leave the projection `drifted` /
   `stale`. That end state is correct — rebuild with the existing command:

   ```sh
   traceary store search-projection rebuild
   ```

7. **Compact** if you need the file to shrink on disk. Encoding returns overflow
   pages to the free list; only `store compact` returns physical bytes:

   ```sh
   traceary store compact
   ```

## Semantics operators should know

| Property | Behaviour |
|---|---|
| Selection | `body_codec IS NULL OR body_codec = 'identity'`, only rows whose five codec columns are all-NULL or all-present |
| Cursor | `events.rowid` (monotonic). Event `id` values are random and must not be used as a cursor |
| High-water | `max(rowid)` fixed at run start. Rows ingested during the run stay identity and are left for a later run |
| Fixpoint | Passes repeat until a full walk rewrites nothing and skips no conflicts |
| Atomic batch | Re-verify source, encode, write body + five codec columns, advance checkpoint — one transaction |
| Partial metadata | Counted, named in the result, run fails closed; the row is not rewritten |
| Provenance | `body_stored_bytes`, `body_original_bytes`, and `source_hook` / `legacy_source_hook` are not written by the backfill; migration 053 carries them across representation changes |
| Projection | Not suppressed. Expect `drifted`/`stale` and rebuild |
| Recipe version | Checkpointed. A different binary recipe refuses resume rather than skip a prefix |

Output is JSON on stdout with aggregate counters only (no body contents).

## Failure and resume

- A crash between batches leaves a valid store and a `running`/`paused`
  checkpoint. Resume continues under the same recipe version.
- A partial-metadata row fails the run closed and names the event id. Fix or
  quarantine that row before starting a new run (a failed run is not resumable;
  start a new `run` after repair).
- Resume always restarts the cursor at the origin of the high-water range so a
  mid-pass pause cannot strand a conflict-skipped row.

## Related docs

- [Payload compression rehearsal](payload-rehearsal.md) — cost prediction on a copy
- [Search projection rebuild](../search-projection-rebuild.md)
- [Safe compaction](safe-compaction.md)
