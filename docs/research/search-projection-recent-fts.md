# Write-only recent FTS (#1842)

[日本語](./search-projection-recent-fts.ja.md)

Scratch measurement of whether the unread recent trigram FTS earns its keep. Not the live ~36 GiB store.

## Decision

**Delete the write-only tier.** Do not re-wire `traceary search` to read it.

`8cae0ab0` removed the last reader. Search is the fingerprint + decode walk. Session tier is exact-keyword + LIKE (#1756). Emptying this FTS does not change results.

## Why not re-wire (B)

Re-wiring brings back snapshot/tail merge (#1718). v0.37 is not a "feels faster" gate. The #1620 store paid 9.0 GB for `search_projection_recent_fts_data` and then hit `database or disk is full`.

## Scratch (`TestRecentFTSDoesNotEarnDecodeWalk`)

120 notes, generation complete. Shipped FTS MATCH is 0 (writers dropped). Comparison trigram FTS is built from `search_projection_recent_documents` only for the table, then dropped.

| query | walk hits | walk p50 | walk p95 | compare FTS hits | FTS p50 | FTS p95 |
|---|---|---|---|---|---|---|
| unique-recent-marker | 1 | 3.69 ms | 3.83 ms | 1 | 33 µs | 45 µs |
| shared-token | 12 | 4.36 ms | 4.57 ms | 12 | 26 µs | 31 µs |

FTS is faster. It does not earn 9 GB of unread postings. Fingerprints still return the same events.

## How

- Migration 066 (`constant_in_place`) drops the two writer triggers. Implicit open does not DROP the virtual table (same rule as 052).
- `store compact` drops `search_projection_recent_fts` on the work copy.
- `--index-family-bytes` still defaults to 1464 MiB. It now targets what remains (documents, session, fingerprints, shared). Leftover FTS pages count until compact.

## Promise

`literal_search_fingerprints` and the session tier keep working. Search does not read the recent FTS.
