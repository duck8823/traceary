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
| Selection | `body_codec IS NULL OR body_codec = 'identity'`, plus any row whose five codec columns are neither all-NULL nor all-present — those are selected precisely so the run can fail closed on them |
| Affinity | zstd bodies are stored as BLOB, identity bodies as TEXT, matching every other writer. SQL that still reads a plaintext body (migration 053's `LIKE`) keeps working |
| Cursor | `events.rowid` (monotonic). Event `id` values are random and must not be used as a cursor |
| High-water | `max(rowid)` fixed at run start. Rows ingested during the run stay identity and are left for a later run |
| Fixpoint | Passes repeat until a full walk rewrites nothing and skips no conflicts |
| Atomic batch | Re-verify source, encode, write body + five codec columns, advance checkpoint — one transaction |
| Partial metadata | Counted, named in the result, run fails closed; the row is not rewritten |
| Provenance | `body_stored_bytes`, `body_original_bytes`, and `source_hook` / `legacy_source_hook` are not written by the backfill; migration 053 carries them across representation changes |
| Projection | Not suppressed. Expect `drifted`/`stale` and rebuild |
| Recipe version | Checkpointed. A different binary recipe refuses resume rather than skip a prefix |

Output is JSON on stdout with aggregate counters only (no body contents).

## Preconditions the command checks for you

- **Counter-mode compatibility state.** A store that applied the *original* migration 36 was left in
  `legacy_index` mode by migration 043. Migration 036's `payload_codec_events_update` trigger updates
  the counter row `WHERE mode='counter' AND state='valid'` and aborts when that matches nothing, so
  on such a store every `identity` → `zstd` transition would abort its batch with `constraint failed:
  invalid payload codec compatibility state`. `preview` and `run` refuse up front and name the mode
  instead, leaving no open run behind.
- **Rehearsal becomes unavailable afterwards.** `store payload-rehearsal` refuses to start once the
  live store holds any non-identity payload. Take whatever rehearsal evidence you want *before* the
  first backfill run.

## Failure and resume

- A crash between batches leaves a valid store and a `running`/`paused`
  checkpoint. Resume continues under the same recipe version.
- A partial-metadata row fails the run closed and names the event id. Fix or
  quarantine that row before starting a new run (a failed run is not resumable;
  start a new `run` after repair).
- Resume always restarts the cursor at the origin of the high-water range so a
  mid-pass pause cannot strand a conflict-skipped row.
- Only one worker may advance a run. A `resume` claims the run by stamping a
  fresh worker token, and every checkpoint asserts both that token and
  `state = 'running'`. A second `resume` therefore fences the first worker: its
  next batch aborts with "run was terminated by another worker" instead of
  interleaving cursor writes and counters into the same run. A failure that
  cannot be recorded is reported the same way rather than as a row-level
  failure the operator could act on.
- Cancelling the process (`Ctrl-C`) persists a `paused` checkpoint before
  returning — whether the cancellation is noticed between batches or inside the
  select or the batch transaction — so `resume` picks it up and `status` does
  not report it active.

## Related docs

- [Payload compression rehearsal](payload-rehearsal.md) — cost prediction on a copy
- [Search projection rebuild](../search-projection-rebuild.md)
- [Safe compaction](safe-compaction.md)
