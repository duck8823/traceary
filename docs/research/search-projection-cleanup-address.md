# Decision: table-shaped cleanup addressing (#1825)

[日本語](./search-projection-cleanup-address.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1825

## Decision

Cleanup addresses each projection table by that table's primary key, not by
`rowid`. `ProjectionCleanupCandidate` carries `Address1/2/3` and
`AddressBlob`. Deletes are built per class. `RowsAffected() != 1` is still
`SearchProjectionDriftError`.

The two `WITHOUT ROWID` conversions from #1808 are **not** applied.

## Why not convert now

`search_projection_session_keywords` still materialises an implicit PK
autoindex about as large as the table (scratch `dbstat` on 400 rows; same
ratio #1808 saw on a 408k-event rehearsal). That ratio is structural.

Converting still means either:

- `INSERT…SELECT` of corpus-proportional tables (store-sized, extra disk peak), or
- `DROP` + recreate + abandon the live generation (every upgraded store
  rebuilds search).

Both are larger than the stall this issue exists to prevent. Addressing
removes the compile-time coupling so a later conversion cannot break
`SELECT rowid`.

## Outcome

- Recent / eviction still use `document_id` (INTEGER PRIMARY KEY).
- Keyword, fingerprint, summary, aggregate, and exclusion use their PK
  columns.
- Rowid tables stay. Autoindex duplication remains until a dedicated
  conversion with a disk-peak plan.
