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

The fixture uses the canonical production migrations and query sources. It retains exactly `--small-rows` generic small rows and `--large-rows` 1 MiB rows, separately creates then deletes 1,000 disposable rows, and adds one active lifecycle, ten command/audit rows, and ten accepted workspace memories. Fixture JSON records these post-delete workload cardinalities. Preflight fails unless active/latest return a matching row and production handoff returns exactly ten recent commands and ten memories.

## Sanitized 21.4 GiB-shape baseline

The pre-migration reference shape is **21.4 GiB allocated database bytes**. Capture evidence from a consistent copy; do not commit the copy or raw rows. A baseline artifact is valid when `capacity.json` records `database_bytes` near 22,978,910,618 bytes, records WAL/free-page values, reports explicit `dbstat` completeness, and `benchmark.json` contains all four cases and their plans. Host, path, identifiers, and query values are deliberately excluded. Timing values are environment-specific and must not be compared across machines as if hardware/cache conditions were equal.

Copy `capacity-baseline.sample.json`, replace every placeholder timing and plan with sanitized measured evidence, then validate it with `go run ./cmd/store-benchmark --validate-baseline ./capacity-baseline.json`. The validator requires the 21.4 GiB shape (within 256 MiB), explicit capacity evidence, positive timings for passed cases, and all four production-query plans. Do not add paths, bound values, host names, or identifiers.

Each case is bounded by `--case-timeout` (default `2m`, minimum `1ms`). Plans are captured before timed execution, so a timeout still includes sanitized `query_plan`, `timeout_ms`, and right-censored `elapsed_lower_bound_us`. Its p50/p95 fields remain unobserved rather than fabricated as completion evidence. The report status must be `timeout` whenever any case times out. The validator accepts this as diagnostic evidence, but only `status: "passed"` with observed p50/p95 satisfies the release performance target.

A completed `search` case includes privacy-safe aggregate `matched_rows`. Zero is valid; no body, query value, or identifier is emitted.

## Exhaustive legacy/tiered search parity

The additive parity mode proves membership-set equality only after exhausting every legacy offset page and every authenticated tiered continuation. It does not change legacy search authority. Supply private criteria as JSON on stdin, or in a regular file whose permissions are exactly `0600`:

```sh
cat /private/tmp/private-parity-manifest.json | \
  go run ./cmd/store-benchmark --search-parity-manifest - > /private/tmp/search-parity.json
go run ./cmd/store-benchmark --validate-search-parity /private/tmp/search-parity.json
```

Required manifest fields are `db_path`, `query`, `legacy_page_size`, `tiered_page_size`, `source_rows`, `stored_bytes`, `decoded_bytes`, `timeout_ms`, `expected_revision`, and `expected_dirty`. Optional filters are `workspace`, `session_id`, `client`, `agent`, `kind`, `from`, `to`, and `failures_only`. Set `expected_revision` from `git rev-parse HEAD`; `expected_dirty` is required and must be `false`, and the worktree must be clean. A mismatch fails before store access. Keep the manifest and generated artifact outside the repository so evidence cannot make its own dirty-state assertion self-referential.

Output uses `traceary.tiered-search-parity/v1` and `comparison_contract: membership_set/v1`. It contains only revision/dirty state, membership and duplicate counts, page and continuation counts, projection revision/high-water, latency or right-censored elapsed lower bounds, budgets, and aggregate logical/physical bytes. Queries, identifiers, paths, cursors, continuations, and raw error text are never emitted. Failures use fixed error classes. Status precedence is `failed > timeout > mismatch > passed`.

The validator rejects unknown or privacy-forbidden fields, trailing JSON, inconsistent metrics, and unknown error classes. A timeout is diagnostic right-censored evidence, not parity proof. Only `passed` proves that both chains completed without duplicates and their final membership sets were equal.
