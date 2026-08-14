# Decision: keep `event_metadata_projection` (#1686)

[日本語](./event-metadata-projection-retention.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1686

## Decision

Do **not** retire `event_metadata_projection`. Do not switch the 21 metadata
read files back to `events`.

The issue's premise was "once `events` is narrow, the projection has no
reason to exist." #1743 measured the narrowing and decided **not** to extract
`events.body`. `events` stays wide. The 2026-08-10 live comparison still
applies.

## Why stage 1 would regress

Same 13 metadata columns, `ORDER BY created_at_norm DESC, id DESC`,
`LIMIT 5000`, best of 3, reference store 2026-08-10 (`sqlite3`,
`PRAGMA query_only=1`, 596,212 events, `events` 6.331 GiB). Cited, not
re-queried in this change.

| Query | Warm | Cold |
|---|---:|---:|
| projection | 0.012 s | 0.042 s |
| `events` metadata, no join | 0.032 s (2.7×) | 0.633 s (15×) |
| `events` id only | 0.005 s | — |

The id-only control shows the index is not the cost. The cost is fetching the
wide row. Covering indexes cannot close the gap inside the space the drop
would reclaim (~0.47 GiB): seven distinct leading-column orders, and the
projection also denormalises `command_audits(exit_code, failed)`.

#1743 scratch (512 × 8 KiB, refused live path): after the codec, repetitive
bodies are already 1.07×; overflow entropy is 1.87× and a side table matches
projection time but **grows** the file. Extraction did not land.

## Space vs the #1620 sheet

Net projection family ≈ 0.47 GiB. The #1620 balance sheet already closed at
~2.57 GiB without this lever. Paying 2.7× / 15× on `list` / session lookup
to reclaim that is the trade this issue asked to re-evaluate. It fails.

## What stays

- All current `FROM event_metadata_projection` readers
- `legacy_source_hook` on the projection (0 rows on the reference store;
  still a compatibility column — do not drop it in this issue)
- Writer triggers that keep the projection current

## Non-goals

- Dropping the table or its indexes.
- Re-running the 21 query shapes against a narrowed `events` (there is no
  narrowed `events`).
- Querying the live store.
