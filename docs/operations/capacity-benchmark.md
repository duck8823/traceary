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

Copy mode opens the input with SQLite `immutable=1&mode=ro`. For handoff, each cold sample creates one fresh immutable connection group shared by every production datasource in `ContextUsecase.Handoff`; warm immediately repeats the full orchestration on that same group. It does **not** claim an OS-cache cold start and never clears host caches. Output uses `traceary.store-benchmark/v1` and includes p50/p95 microseconds plus `EXPLAIN QUERY PLAN` details for `active`, `latest`, `handoff`, and `search`.

Create the bounded synthetic fixture with:

```sh
go run ./cmd/store-benchmark --synthetic /private/tmp/traceary-synthetic.db \
  --small-rows 10000 --large-rows 8 --iterations 25 > synthetic-benchmark.json
```

The fixture uses the canonical production migrations and query sources. It retains exactly `--small-rows` small rows and `--large-rows` 1 MiB rows, and separately creates then deletes 1,000 disposable rows to guarantee free-page pressure without falsifying fixture metadata. The `handoff` timing executes the production `ContextUsecase.Handoff` orchestration; its plan evidence labels the shared production session-resolution and command-preview SQL constituents.

## Sanitized 21.4 GiB-shape baseline

The pre-migration reference shape is **21.4 GiB allocated database bytes**. Capture evidence from a consistent copy; do not commit the copy or raw rows. A baseline artifact is valid when `capacity.json` records `database_bytes` near 22,978,910,618 bytes, records WAL/free-page values, reports explicit `dbstat` completeness, and `benchmark.json` contains all four cases and their plans. Host, path, identifiers, and query values are deliberately excluded. Timing values are environment-specific and must not be compared across machines as if hardware/cache conditions were equal.

Copy `capacity-baseline.sample.json`, replace every placeholder timing and plan with sanitized measured evidence, then validate it with `go run ./cmd/store-benchmark --validate-baseline ./capacity-baseline.json`. The validator requires the 21.4 GiB shape (within 256 MiB), explicit capacity evidence, positive timings, and all four production-query plans. Do not add paths, bound values, host names, or identifiers.
