# Decision: verify `recent_source_bytes`, do not trigger-maintain it (#1819)

[日本語](./recent-source-bytes-verifier.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1819

## Decision

Report the cache against ground truth on `traceary store search-projection
status`. Do **not** move the counter into `AFTER INSERT` / `AFTER DELETE`
triggers (option 1) and do **not** rewrite the cache from the verifier.

`recent_source_bytes` stays the ApplyBatch increment. Status now also emits:

- `recent_source_bytes` — persisted cache
- `recent_source_bytes_measured` — `SUM(decoded_bytes)` for
  `search_projection_state.generation_id`
- `recent_source_bytes_delta` — cache minus measured
- `recent_source_bytes_evidence` — `complete` / `sum` when the SUM ran

A non-zero delta is the drift the issue said was invisible. Status does not
correct it. Eviction still reads the cache.

## Why not option 1

Option 1 is the only unbreakable form, and it adds a singleton-row UPDATE on
the hottest rebuild write. The issue says measure first. No released binary
can produce this drift (#1817 already reset mid-rebuild leftovers). v0.36
takes the no-hot-path verifier.

## Why this SUM, not `recent_bytes`

`recent_bytes` already sums the **active** generation. During a rebuild the
cache is written against `generation_id` (incoming). Comparing those two
would false-alarm on every rebuild. The verifier uses the same generation
ApplyBatch increments.

## Non-goals

- Trigger-maintained counter.
- Reconcile-and-correct at the source→eviction boundary.
- A doctor check (2 GiB stores stay metadata-only and must not SUM the recent
  tier).
- Querying the live store.
