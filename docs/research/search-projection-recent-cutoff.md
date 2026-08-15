# Search-projection recent-cutoff row cap (#1807)

[日本語](./search-projection-recent-cutoff.ja.md)

Scratch-sized measurement of the source-phase prefilter. Not the live ~36 GiB store.

## Decision

Bound the walk by **newest-row count** (`20_000`), not a 2s wall-clock timeout. Do not add a flag. Do not retry with a wider deadline.

The 2s timeout succeeded on small stores (which do not need a cutoff) and failed on large ones (which do), persisting empty `recent_cutoff_norm` and age-only admit-then-evict.

## Algorithm

1. Ceiling ≤ 0 → retain-nothing sentinel (unchanged).
2. Window the newest N rows. Crossing → that `created_at_norm`.
3. No crossing and sample `< N` → empty cutoff, empty reason (corpus fits).
4. No crossing and sample `== N` → cutoff = oldest sampled timestamp.
5. Cancel / query error → empty cutoff, `recent cutoff: …` (existing degrade).

Eviction still enforces the exact ceiling. A sample-tail cutoff may exclude older rows that would have fit; that only cuts build cost.

## Tests

- `TestSearchProjectionRecentCutoffUsesSampleTailWhenRowCapMissesCrossing`
- `TestSearchProjectionCutoffFailureDegradesNotBreaks` (cancel still degrades)
- `TestSearchProjectionCutoffUsesBlobByteLength` (LIMIT bind)
