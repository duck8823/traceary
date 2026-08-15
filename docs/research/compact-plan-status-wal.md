# Compact plan then status WAL pair (#1848)

[日本語](./compact-plan-status-wal.ja.md)

Scratch measurement of why `store search-projection status` can leave a 0-byte `-wal` and a 32,768-byte `-shm` after a prior `store compact plan`. Not the live store.

## Decision

**Explain, do not change the open path.** #1845 already recovers that stale pair on the next compact preflight. A second checkpoint or journal-mode change on status is not added.

## What the scratch shows (`TestCompactPlanThenStatusLeavesEmptyWALPair`)

Same process, WAL DSN as production (`journal_mode=WAL`). Sidecar sizes are taken with `Stat` only — a read-only open of a WAL-mode file creates the same pair.

| step | `-wal` | `-shm` |
|---|---|---|
| after `search-retire` | absent (0) | absent (0) |
| after `compact plan` | absent (0) | absent (0) |
| `status` after retire only | 0 | **32,768** |
| `status` after plan | 0 | **32,768** |

`compact plan` does not leave a pair (it cleans stale sidecars, then inspects). `status` uses `sqliteDSN`, which sets `journal_mode(WAL)`. Closing that connection leaves the empty WAL sidecar. The pair is not plan-only on this scratch.

The original CLI ordering (`log` → `search-retire` → `status` showed no pair; inserting `compact plan` did) is the same sidecar: plan removes whatever was there, then status creates a fresh empty pair that the next compact preflight sees.

## Why not fix

#1845 already treats 0-byte WAL + 32 KiB SHM as stale and removes it under the exclusive lease. Status is a read of projection bookkeeping; forcing DELETE mode or a checkpoint on every status would change the production WAL contract for a harmless leftover.

## Promise

The pair is a WAL-mode sidecar from `status`, not a write and not a compact-plan journal-mode flip.
