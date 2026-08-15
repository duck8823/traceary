# `--recent-age` binding measurement (#1755)

[日本語](./search-projection-recent-age.ja.md)

Scratch-sized measurement of which recent-tier cutoff binds. Not the live ~36 GiB store.

## Decision

**Keep `--recent-age`.** Do not add a third flag. Removal would need the admin one-minor deprecation window.

Retention is `created_at > max(now − age, byteCutoff)` (`RecentRetentionCutoff` / `PlanProjectionBatch`).

## Scratch corpora (`TestRecentAgeBindingOnScratchCorpora`)

`now = 2026-06-30T00:00:00Z`, age = 30 days (age cutoff = 2026-05-31).

| shape | `RecentCutoffNorm` | binds | retained |
|---|---|---|---|
| dense ingest | 2026-06-20 | **byte** | 2026-06-25 yes; 2026-06-10 no (age-only); 2026-05-20 no |
| quiet store | empty (walk never crossed) | **age** | 2026-06-15 yes; 2026-05-20 no |

On a store with capacity pressure the byte cutoff is newer, so the 30-day flag does not shrink the window. Age binds only when the recent tier is already small. Touch `--recent-age` to drop old rows on a quiet store for a reason other than the index-family budget.
