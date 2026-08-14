# Decision: a body side table is not worth extracting (#1743)

[日本語](./body-side-table-locality.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1743

## Decision

Do **not** move `events.body` to a side table.

The locality claim is real on overflow-sized rows, and a scratch side table
matches projection-class metadata time. It is still not worth the schema:
current metadata reads already avoid `events` via `event_metadata_projection`,
the file does not shrink, and the mutation cost listed on #1743 stays unpaid.

## Method

`go run ./cmd/store-benchmark --measure-body-locality DIR` builds two scratch
stores per corpus (never the live path):

1. **inline** — production `EventDatasource.Save` (canonical codec, including
   zstd when it shrinks).
2. **side_table** — copy, `event_bodies(event_id, body)`, `UPDATE events SET
   body=''`, `VACUUM`.

Queries are the #1686 pair (metadata columns, `ORDER BY created_at_norm DESC,
id DESC`, `LIMIT 5000` / `200`) plus an id-only control. Gate (warm p50,
`LIMIT 5000`):

- method valid: inline / projection ≥ 2.0
- material: method valid, no table-scan regression, inline / side ≥ 2.0, and
  side / projection ≤ 1.5

Seed `1743`. Default 256 × 8 KiB. The table below is 512 × 8 KiB, 7 iterations,
`modernc.org/sqlite`.

## Scratch numbers (2026-08-15)

| Corpus | Layout | DB bytes | `events` pages (dbstat) | projection warm | events warm | ratio |
|---|---|---:|---:|---:|---:|---:|
| entropy | inline | 3.70 MiB | 2.10 MiB | 445 µs | 831 µs | 1.87× |
| entropy | side | 3.83 MiB | 0.11 MiB | — | 449 µs | 1.01× vs projection |
| repetitive | inline | 1.75 MiB | 0.14 MiB | 466 µs | 499 µs | 1.07× |
| repetitive | side | 1.79 MiB | 0.12 MiB | — | 457 µs | 0.98× vs projection |

Plans stay index scans (`idx_event_metadata_created_at_norm_id_desc` /
`idx_events_created_at_norm_id_desc`). Id-only covering scans are 67–84 µs
either layout — the extra inline cost is the table row, not the index.

Entropy almost meets the 2× method bar; repetitive does not, because the
codec already pulled those bodies out of overflow. Side table **grows**
resident bytes in both corpora (bodies are copied, not removed from the
file). That is why the harness decision is `not_material`.

## Live store (cited, not queried)

2026-08-10 on the reference store (`sqlite3`, `PRAGMA query_only=1`, 596,212
events, `events` 6.331 GiB, recorded on #1686):

| Query | Warm | Cold |
|---|---:|---:|
| projection `LIMIT 5000` | 0.012 s | 0.042 s |
| `events` metadata `LIMIT 5000` | 0.032 s (2.7×) | 0.633 s (15×) |
| `events` id only | 0.005 s | — |

That is why the projection exists. It is not a reason to extract bodies while
the projection remains, and it does not pay the #1743 mutation list.

## Costs that stay unpaid

If the gate had flipped, extraction would still have to account for:

- `ON DELETE CASCADE` / explicit event-id rewrite (migration 034)
- a cross-table “has a body” invariant SQLite `CHECK` cannot express
- three `UPDATE OF body` trigger families (038 / 039 / 041)
- four silent content-loss consumers (canonical audit, archive segment,
  archive restore, bundle)
- `legacy_index` codec compatibility keyed on `events.body_codec`
- `command_audits` locality: the projection denormalises exit/failed, so
  narrowing `events` alone does not retire it

## Non-goals

- Querying `~/.config/traceary/traceary.db`.
- Shipping `event_bodies` or dropping `events.body`.
- Retiring `event_metadata_projection` (#1686).
