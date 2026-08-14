# Decision: separate lock wait from row work (#1833)

[日本語](./search-projection-lock-vs-row-work.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1833

## Decision

Time write-lock **acquisition** and lock **hold** on different clocks.

`BEGIN IMMEDIATE` uses the remaining lock-duration window. Only time spent
after that lock is held is row work. Contention that never obtains the lock
stays `lock_duration_cap_exceeded` and is not excluded. A single source write
that overruns the hold budget is skipped as `class=row_work`, matching #1794.

`LockTime` stays out of `ConfigHash`. Inventory `BeginTx` is unchanged.

## Why not persist after N single-row failures

The checkpoint advances in the same transaction as the work. A store under
writer contention sits at the same checkpoint as a slow row. Excluding on a
counter would drop good events.

## Why SQLITE_BUSY is not the signal

`busy_timeout` is 1000 ms and `LockTime` is 250 ms. The acquire context
expires before the driver returns busy, so contention and a slow row used to
look identical.

## Outcome

- Other connection holds `BEGIN IMMEDIATE` → acquire fails, no exclusion.
- Hold overrun on exactly one source write → `row_work` exclusion, checkpoint
  advances. Identity comes from the failed plan, not a new snapshot.
- Neither condition → same catch-up behaviour as before.
