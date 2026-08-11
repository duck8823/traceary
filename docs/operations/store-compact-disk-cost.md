# Store compact disk cost

[日本語](store-compact-disk-cost.ja.md)

`traceary store compact` needs free disk space for the source database and the
candidate that `VACUUM INTO` writes. Its preflight reserves **1.1 times the
store size** (plus any operation-specific temporary budget) before it starts.
This is a deliberate worst-case reservation: SQLite cannot report the
candidate size before writing it, so the safe assumption is that the compacted
store may be as large as the source. In practice, compaction can produce a
much smaller file, but that smaller result is not safe to reserve in advance.

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
