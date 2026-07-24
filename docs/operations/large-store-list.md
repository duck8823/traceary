# Bounded metadata list on large live stores

[日本語](large-store-list.ja.md)

For a quick, body-free indication of the newest event on a large or actively
written store, use:

```sh
traceary list --limit 1 --fields ts,kind --color never
```

This exact unfiltered shape uses the bounded latest-metadata path. It opens
SQLite read-only, does not initialize or migrate the store, does not run the
workspace-observation catch-up, and does not read event bodies, prompts,
responses, command payloads, hook spools, credentials, or identifier samples.
It uses the existing timestamp index and only normalizes timestamps that share
the newest second, preserving correct RFC3339Nano ordering without a
whole-store sort.

## Interpret the result

- A row is a completed metadata result, not a health assertion for every
  subsystem.
- A `busy` or `locked` SQLite error is lock contention. It is distinct from a
  slow query: stop or isolate the competing writer, then retry the same bounded
  command. Do not inflate timeouts or repeat `doctor --fix` as a lock remedy.
- Adding `message`, using `--wide`, using `--sensitive`, or adding filters uses
  the normal read path. Those forms may initialize an old store and can take
  longer; use them only after a bounded check succeeds.
- This command does not delete data, apply retention, build an index, or modify
  SQLite sidecars. For capacity planning, preview the separate operation with
  `traceary store gc --dry-run` and archive before applying retention.

## Rollback

The feature has no schema or data migration. Reverting the release restores the
former full initialization path; it does not change stored events.
