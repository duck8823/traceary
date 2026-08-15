# Decision: implicit open refuses data-dependent migrations (#1852)

[日本語](./offline-migration-gate.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1852

## Decision

`migrate()` reads `MigrationExecutionClass`. A `data_dependent_offline`
migration is not applied on implicit store open when the store already has
source events. The error names the pending versions and `traceary store init`.
The store is not modified at the refused migration.

`traceary store init` is the only operator-authorized apply path. No new
command. A new empty store still reaches the current schema on implicit open.

041 and 042 are `constant_in_place`: they copy projection bookkeeping, not
`events`. 035 and 045 remain offline (`CREATE INDEX` on existing data).

## Why implicit refuse

#1851 showed that a killed one-transaction migration restarts from zero on
every open. Classification that nothing consults is a comment. A hook that
blocks for 60 s and then retries forever is worse than an immediate
maintenance error.

## Why not a silent empty read

`list` / `search` return the typed error. Returning zero rows would hide that
the store is behind.

## Outcome

- v0.33.1-shaped store + events: implicit open fails, ledger stays at 34.
- Same store + `store init`: reaches the current version.
- Empty store: implicit open reaches the current version.
- After 041/042 reclassification, implicit open applies them and stops at 045.
- Interrupted 045: ledger stays at 44; the next implicit open fails immediately.
