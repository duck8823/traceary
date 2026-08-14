# Payload compression backfill (live store)

[日本語](payload-backfill.ja.md)

> Removed in v0.35.0 (#1872). Encoding now happens during
> `traceary store compact`. `store payload-backfill` is an unknown command.

`traceary store payload-backfill` rewrote every existing compressible payload
text column through the versioned zstd codec already shipped in
`payload_codec.go`:

| Table | Columns |
|---|---|
| `events` | `body` |
| `command_audits` | `command_text`, `input_text`, `output_text` |

It is the operator command that applies compression to retained history.

This is **not** `store payload-rehearsal`. Rehearsal writes only to a shadow
table on a *copy*, freezes inserts, and refuses to start once the live store
holds any non-identity payload. It predicts cost; it cannot perform the work.
Backfill mutates the live store in place, never freezes writers, and treats a
mixed identity/zstd corpus as a fully valid store at every batch boundary.

## When to run it

- After upgrading to a build that includes migration 053 (codec-aware body
  provenance triggers) and 054 (backfill bookkeeping).
- Before relying on the #1620 store-size gate numbers that assume compressed
  event bodies **and** compressed command-audit text.
- On a maintenance window where you can rebuild the search projection and run
  `store compact` afterwards.

## Procedure

1. **Backup** the live store (for example `traceary store backup create ~/traceary-pre-v0.34.db`).
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
   `stale`. Audit-text rewrites do not drive projection invalidation (no
   search writer reads them after migration 052), but a rebuild is still
   required whenever body rows changed. There is no single `rebuild` verb —
   start a new generation, then repeat `resume --until-complete` until
   `status` reports the generation as `complete`. A run that reports
   `stop_reason=max_batches` or `stop_reason=total_wall_time` is not complete;
   run `resume --until-complete` again before continuing:

   ```sh
   traceary store search-projection start
   traceary store search-projection resume --until-complete
   traceary store search-projection status
   ```

7. **Compact** if you need the file to shrink on disk. Encoding returns overflow
   pages to the free list; only `store compact` returns physical bytes:

   `store compact` on its own prints help and compacts nothing, and `plan`
   refuses while the legacy migration-032 index is still resident — including
   on a store created by this version, see
   [#1847](https://github.com/duck8823/traceary/issues/1847). The full
   sequence is:

   ```sh
   traceary store search-retire
   traceary store compact plan          # prints the run id
   traceary store compact apply RUN_ID
   traceary store compact status RUN_ID
   ```

## Semantics operators should know

| Property | Behaviour |
|---|---|
| Selection | Per field: `<field>_codec IS NULL OR <field>_codec = 'identity'`, plus any field whose five codec columns are neither all-NULL nor all-present — those are selected precisely so the run can fail closed on them |
| Lanes | `events.body`, then `command_audits.{command,input,output}_text`. One shared rowid cursor walks both tables; a batch loads every eligible field of each selected rowid so a limit cut cannot strand a sibling field |
| Affinity | zstd values are stored as BLOB, identity values as TEXT, matching every other writer. SQL that still reads a plaintext body (migration 053's `LIKE`) keeps working |
| Cursor | Shared numeric `rowid` cursor across `events` and `command_audits` (each table's rowid sequence is independent). Event `id` / audit `event_id` values are random and must not be used as a cursor |
| High-water | Per-table ceilings fixed at run start: `high_water_rowid` = `MAX(events.rowid)`, `audit_high_water_rowid` = `MAX(command_audits.rowid)`. Each table is bounded by its own ceiling so a later insert into the lagging sequence cannot land below a shared max. Resume reads both from the checkpoint and never recomputes them |
| Scanned rows | Counts **lane candidates** examined (one `events.body` or one `command_audits` text field), not physical table rows. An audit row with three eligible fields contributes 3. The JSON field name `scanned_rows` is frozen |
| Fixpoint | Passes repeat until a full walk rewrites nothing and skips no conflicts |
| Atomic batch | Re-verify source, encode, write field + five codec columns, advance checkpoint — one transaction |
| Partial metadata | Counted, named in the result (event id / audit event_id), run fails closed; the field is not rewritten |
| Provenance | `body_stored_bytes`, `body_original_bytes`, `source_hook` / `legacy_source_hook`, and `input_original_bytes` / `output_original_bytes` are not written by the backfill; migration 053 carries body provenance across representation changes |
| Projection | Not suppressed for body rewrites. Expect `drifted`/`stale` and rebuild |
| Recipe version | Checkpointed. A different binary recipe refuses resume rather than skip a prefix. Current recipe: `events-body-command-audits-zstd-v2` |

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
- Cancelling the run's context persists a `paused` checkpoint before returning
  — whether the cancellation is noticed between batches or inside the select or
  the batch transaction — so `resume` picks it up and `status` does not report
  it active. Every terminal transition (complete, reset, pause, fail) runs on a
  context the cancellation cannot reach, because it records what the worker
  already did durably. That context is bounded, so a checkpoint that cannot
  take the store lease gives up instead of hanging.
- Killing the process instead (`Ctrl-C` on the CLI, which does not currently
  wire signals into the command context — see #1747) writes no checkpoint. The
  in-flight batch rolls back and the run stays at `running`; `resume` still
  continues it from the last committed batch, but `run` refuses until then
  because `status` reports it active.
- A cancellation does not rename the error. If an I/O, constraint or decode
  failure happens to race the `Ctrl-C`, the checkpoint still lands but the
  reported error is the failure, not "cancelled".

## Related docs

- [Payload compression rehearsal](payload-rehearsal.md) — cost prediction on a copy
- [Search projection rebuild](../search-projection-rebuild.md)
- [Safe compaction](safe-compaction.md)
