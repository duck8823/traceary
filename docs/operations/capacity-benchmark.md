# Capacity diagnostics and benchmark evidence

[日本語](capacity-benchmark.ja.md)

`traceary store capacity` emits `traceary.capacity/v1` JSON. The report contains only page counts, file sizes, SQLite object names, and aggregate payload-size buckets. It does not select or emit event bodies, prompts, transcripts, command payloads, or workspace/session/event identifiers.

```sh
traceary store capacity --db-path ./traceary-copy.db > capacity.json
```

`evidence.status` is `complete` when SQLite's optional `dbstat` virtual table is available. Otherwise it is `unavailable`, `evidence.method` is `pragma`, and `objects` is omitted. Unexpected dbstat errors fail the command. Payload buckets use `event_metadata_projection.body_stored_bytes`; `payload_evidence.status` is `partial` while that projection is not fully backfilled.

## Safe benchmark workflow

Never benchmark the live store. Create a consistent copy first:

```sh
traceary store backup create /private/tmp/traceary-benchmark.db
go run ./cmd/store-benchmark --db /private/tmp/traceary-benchmark.db --iterations 25 > benchmark.json
```

Copy mode opens the input with SQLite `immutable=1&mode=ro`. “Cold” means a new SQLite connection per sample; it does **not** claim an OS-cache cold start and never attempts to clear host caches. “Warm” is the immediately repeated query on that connection. Output uses `traceary.store-benchmark/v1` and includes p50/p95 microseconds plus `EXPLAIN QUERY PLAN` details for `active`, `latest`, `handoff`, and `search`.

Create the bounded synthetic fixture with:

```sh
go run ./cmd/store-benchmark --synthetic /private/tmp/traceary-synthetic.db \
  --small-rows 10000 --large-rows 8 --iterations 25 > synthetic-benchmark.json
```

The fixture uses the canonical production migrations and query sources. It contains at least 1,001 small rows, a few 1 MiB rows, an uncheckpointed WAL, and deleted rows that create free-page pressure. Smaller `--small-rows` values are rejected because they cannot guarantee free pages. It uses generated strings only.

## Sanitized 21.4 GiB-shape baseline

The pre-migration reference shape is **21.4 GiB allocated database bytes**. Capture evidence from a consistent copy; do not commit the copy or raw rows. A baseline artifact is valid when `capacity.json` records `database_bytes` near 22,978,910,618 bytes, records WAL/free-page values, reports explicit `dbstat` completeness, and `benchmark.json` contains all four cases and their plans. Host, path, identifiers, and query values are deliberately excluded. Timing values are environment-specific and must not be compared across machines as if hardware/cache conditions were equal.

Copy `capacity-baseline.sample.json`, replace every placeholder timing and plan with sanitized measured evidence, then validate it with `go run ./cmd/store-benchmark --validate-baseline ./capacity-baseline.json`. The validator requires the 21.4 GiB shape (within 256 MiB), explicit capacity evidence, positive timings, and all four production-query plans. Do not add paths, bound values, host names, or identifiers.
