# Store compact disk cost

[日本語](store-compact-disk-cost.ja.md)

`traceary store compact` needs free disk space for the source database and the
candidate that `VACUUM INTO` writes. When this volume can hold a source-sized
work copy, preflight still reserves **1.1 times the store size**. When it
cannot, preflight falls back to a dest-sized reservation (source minus a
metadata-only reclaimable-bytes estimate, with a 10% floor) and, if even that
replica cannot fit, an in-place filter plus `PRAGMA incremental_vacuum`
(no rollback inode).

To keep the source-sized work copy off the live volume, pass `--work-dir`
on another disk that can hold the current store:

```text
traceary store compact --work-dir /volumes/external/traceary-compact
```

The candidate is still written beside the live store (dest-sized free space
on the live volume), then exchanged. Rollback remains `<db>.rollback-<run id>`
next to the source except for in-place runs (`compact_strategy=in_place`,
`rollback_retained=false`).

The reservation is not the complete disk cost. After a successful run, the
original database remains as the recovery copy:

```text
<db>.rollback-<run id>
```

It is the size of the source database, and nothing removes it automatically.
The operator decides when it is safe to delete. Deleting it gives up
`traceary store compact rollback RUN_ID` for that run; keep it until the new
database has been inspected and accepted.

For example, in the 30 GB rehearsal from issue #1790, preflight required
36,539,352,678 bytes. The compacted database was 3,094,962,176 bytes, or
9.3% of the source, while a 33,217,593,344-byte
`rehearsal.db.rollback-e59b9aca6ac60852432f077621f09001` remained after the run
committed. The reservation was intentionally conservative, and the rollback
copy was intentionally retained; neither cost is removed automatically.
