# Decision: calibrate storage gates on synthetic corpora (#1811)

[日本語](./storage-gate-calibration.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1811

## Decision

The v0.34 #1620 figures were one maintainer store. Gate rows that depend on
corpus shape are now a **range** produced by `go run ./cmd/store-benchmark
--calibrate-gates DIR`.

| Corpus | Dimension |
|---|---|
| `tiny` | many short unique records (page slack) |
| `enormous` | few large records (per-row budget / exclusion) |
| `cjk` | multi-byte, trigram-dense text |
| `entropy` | high uniqueness (hashes) |
| `repetitive` | compression / dedupe best case |

Generation is deterministic (seed `1811`). No sample data is committed. Writers
are production `EventDatasource.Save` (canonical payload codec). Measurement
uses `CapacityInspector` and `OperatorCostInspector` — the same inspectors as
`traceary store capacity` and `traceary doctor`.

Search-index amplification (`deriveSearchProjectionCapacity`, fallback 2.16x)
stays `unmeasured` in this harness unless a completed search-projection
generation already exists. The rebuild path refuses a measured ratio below
8 MiB of recent-tier sample. That is a separate operator benchmark, not a
default `go test` case.

## Non-goals

- Querying the live store.
- Running the five-corpus job inside `go test ./...`.
- Changing the fallback 2.16x constant in this issue.
