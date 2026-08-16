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
migrations, list events, read event bodies or command payloads, or print
credentials or identifier samples.

The bounded report still includes host package identity: the `*-plugin-version`
family plus the native Grok/Kimi plugin activation checks. These read only
host manifests, host plugin caches, and host CLI probes (`grok inspect --json`,
`kimi.plugin.json`, and similar) — never the Traceary store — so a large live
store is not a reason a stale host package goes unreported. Every other
per-client check (config resolution, event coverage, hook routes) stays
excluded from the bounded report.

## Interpret the result

- **Capacity:** the warning says the live store is large. It is not a 1 GiB
  limit and it does not delete anything. Start with
  `traceary store compact`. Fold sessions you want to reclaim first; `--force`
  writes mechanical summaries and states the loss of agent reasoning.
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
