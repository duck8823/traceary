# Session-tier index vs LIKE (#1756)

[日本語](./search-projection-session-tier-index.ja.md)

Scratch-sized measurement of whether the session tier needs a unicode61+porter FTS5 index. Not the live ~36 GiB store.

## Decision

**Keep exact-keyword + `LIKE`.** Do not add a session-tier FTS. No second, unbudgeted index. No new flag.

`SearchSessionPage` stays on `search_projection_session_keywords` exact match or `summary_text LIKE '%q%' ESCAPE '\'`.

## Why

1. The issue's stemming example is not a LIKE miss: `LIKE '%deploy%'` matches `deployed`.
2. The comparison index charges `--index-family-bytes`. On this scratch it is 290,816 bytes — the same order as the rest of the family (352,256). Those bytes would come out of the recent window at the 1464 MiB ceiling.
3. LIKE recall is not worse than porter FTS on the labeled relevant sets. FTS wins precision (`undeploy` is a substring extra) and folds diacritics (`cafe` ↔ `café`). That is not enough to pay a family-budget index.

## Scratch corpus (`TestSessionTierKeepsLikePathAndMeasuresPorterFTS`)

2,009 session summaries (9 labeled + 2,000 fillers). Query set from existing session-tier tests plus the issue's quality cases. Porter FTS is a comparison table (`search_projection_session_compare_fts`), then dropped. Schema does not grow `search_projection_session_*fts*`.

| query | LIKE hits | LIKE recall | LIKE p50 | LIKE p95 | FTS hits | FTS recall | FTS p50 | FTS p95 |
|---|---|---|---|---|---|---|---|---|
| unique-session-marker | sess-unique | 1.00 | 2.54 ms | 2.73 ms | sess-unique | 1.00 | 62 µs | 68 µs |
| filter-needle | sess-filter | 1.00 | 2.44 ms | 2.52 ms | sess-filter | 1.00 | 9 µs | 10 µs |
| subsecond-marker | sess-subsecond | 1.00 | 2.51 ms | 2.68 ms | sess-subsecond | 1.00 | 9 µs | 9 µs |
| deploy | deployed, deploy, DEPLOY, **undeploy** | 1.00 | 2.42 ms | 2.53 ms | deployed, deploy, DEPLOY | 1.00 | 9 µs | 10 µs |
| Deploy | same as deploy | 1.00 | 2.44 ms | 2.77 ms | same as deploy (no undeploy) | 1.00 | 9 µs | 9 µs |
| cafe | cafe only | 1.00 | 2.41 ms | 2.75 ms | cafe + café | 1.00 | 8 µs | 9 µs |
| café | café only | 1.00 | 2.40 ms | 2.63 ms | café + cafe | 1.00 | 9 µs | 11 µs |

Relevant set for `deploy` is `{deployed, deploy, DEPLOY}`. LIKE extra is `undeploy` (substring). `cafe` / `café` are ASCII-fold only on the shipped path, same as keywords.

| point | dbstat |
|---|---|
| family before compare FTS | 352,256 |
| compare porter FTS (shadow included) | **290,816** |
| family after drop | 352,256 |

At the 1464 MiB ceiling those 290,816 bytes are recent-window bytes the family cannot keep. A multi-year session tier would make the index larger, not cheaper. LIKE latency here is a 2,009-row scan, not a 36 GiB store; the release still does not add an unbudgeted index to buy that scan.

## Promise

The session tier stays exact-keyword + LIKE. If a later minor adds an index, it counts against `--index-family-bytes`.
