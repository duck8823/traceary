# Search-projection rebuild disk peak (#1753)

[日本語](./search-projection-rebuild-peak.ja.md)

Scratch-sized measurement of the two-generation rebuild peak. Not the live ~36 GiB store.

## Decision

**Accept the peak.** `--index-family-bytes` is a steady-state target for one completed generation after cleanup. The release does not bound disk during a rebuild.

Rejected:

- **Reclaim before rebuild** — search would go dark for the whole rebuild. That is why two generations exist.
- **Reserve the peak inside the budget** — the #1620 lower bound was about 10× the 1.43 GiB target; reserving that empties the recent window.
- **A second, larger rebuild ceiling** — another operator knob. v0.37 does not add it.

Operator lever stays `traceary store search-projection abort`. `store compact` cannot reclaim in-use pages.

## Scratch fixture (`TestSearchProjectionRebuildPeakExceedsConfiguredBudget`)

12 short events. Gen1 completed at 64 MiB budget, then gen2 started at a budget below the resident family.

| point | index-family dbstat | store file |
|---|---|---|
| gen1 complete | 258,048 | 761,856 |
| after `Start` (old generation still resident) | 258,048 | 761,856 |
| rebuild peak (during gen2) | **405,504** | 761,856 |
| gen2 complete | 266,240 | 761,856 |

Gen2 budget was 225,280. Peak family was 1.80× that target. File size did not move (no `VACUUM`; WAL/freelist stay in the file).

## Large-corpus lower bound (already documented, #1620)

408,893-event store, 1.43 GiB limit: family 3.99 GB → 10.32 GB → **≥ 14.31 GB** then `database or disk is full`. That 14.31 GB is a lower bound, not a peak, and the copy held four generations.

## Promise

The budget does not bound rebuild disk. Size free space for two generations plus FTS5 inverse postings, not for `--index-family-bytes`.
