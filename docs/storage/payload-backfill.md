# Payload compression backfill (retired)

[日本語](payload-backfill.ja.md)

> Retired in v0.48 (#2264). `store compact` encodes remaining
> `command_audits` text columns (and event bodies) with the payload codec.
> `store payload-backfill` remains an unknown command (removed in v0.35.0, #1872).

The live-store `PayloadBackfillDatasource` is gone. Migration 054's
`payload_backfill_runs` table stays (migrations are append-only) and is unused.
Encoding happens on the compaction work copy; there is no separate operator
command, flag, or opt-in.

`traceary store compact` JSON reports the step as `steps.audit_encode`.
Take a backup first. Rows that already carry codec metadata are left alone.
