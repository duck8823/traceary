# Search-projection `fts_logical_bytes` integer width (#1787)

[日本語](./search-projection-fts-logical-bytes.ja.md)

Scratch-sized measurement of the FTS shadow-table logical-byte query. Not the live ~36 GiB store.

## Decision

Keep the field name. Count integer columns as **8 bytes each** (SQL INTEGER / int64 width). Do not switch this field to `dbstat` (`physical_bytes` already is). Do not only rename it.

`length(CAST(<integer> AS BLOB))` is the decimal digit count (`pgno=123456` → 6). Blob and text columns keep `length(CAST(x AS BLOB))`.

## Shadow columns (probed)

| table | length | 8-byte integer |
|---|---|---|
| `*_fts_data` | `block` | `id` omitted (never in this SUM) |
| `*_fts_idx` | `term` (BLOB) | `segid`, `pgno` |
| `*_fts_docsize` | `sz` (BLOB; not an integer) | `id` |
| `*_fts_config` | `k`; non-integer `v` | `v` when `typeof(v)='integer'` |

## Test

`TestSearchProjectionFTSLogicalBytesUsesIntegerColumnWidth` drives `SearchProjectionStatus` after inserting recent documents. The expected total is walked in Go with the 8-byte rule. The old digit-count SUM must differ on this fixture.

This number is reported, not a budget.
