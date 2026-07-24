# Bounded `doctor` behavior for large live stores

[日本語](large-store-doctor.ja.md)

`traceary doctor --json` is the first safe command when a live SQLite store is
slow or suspected to be lock-contended. For a regular store file of 2 GiB or
more, it returns a bounded metadata-only report:

```sh
traceary doctor --json --warnings-ok
```

The report has `mode: "metadata_only_large_store"` and the
`large-store-diagnostics` warning. This is a completed result, not missing
output. It uses filesystem metadata only; it does not open SQLite, run
migrations, list events, read event bodies or command payloads, inspect hook
spools, or print credentials or identifier samples.

## Interpret the result

- **Capacity:** the warning says the live store is large. It is not a 1 GiB
  limit and it does not delete anything. Start with
  `traceary store gc --dry-run`; archive before applying retention.
- **Lock contention:** the metadata-only result deliberately does not claim
  that the database is unlocked. Stop or isolate competing writers before a
  content-level investigation. Do not keep rerunning `doctor --fix` during a
  busy incident.
- **Deep investigation:** use a reviewed bounded copy or a narrowly scoped
  read command after contention is resolved. Do not copy raw event bodies,
  prompt/response payloads, credentials, or identifier lists into incident
  reports.

The default command never changes data in this mode. `--fix` retains its normal
meaning for small stores, but does not turn the metadata-only outcome into a
retention operation.
