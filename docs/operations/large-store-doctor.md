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

The default command never changes data in this mode. `--fix` keeps the report
metadata-only but acts on the hook spool: it requeues transient dead letters,
drains pending records through spool replay, and prunes dead-letter files and
orphan `*.tmp` leftovers older than 14 days. Spool replay may open SQLite for
event writes only — it never walks dbstat, event bodies, or store capacity —
and `--fix --dry-run` opens nothing.

The whole `--fix` apply phase — dead-letter requeue, the pending-record drain
including SQLite replay, and any later automatic fixes — runs under one shared
45-second wall clock. The drain checks the wall before claiming each record;
an in-flight replay is allowed to finish, so the wall carries a one-record
slack, but no new SQLite replay starts after it. When the wall is reached with
records still pending, the fix action reports the leftover as `remaining=N`,
and later auto-fixable checks are skipped with
`skip: doctor --fix wall exhausted`. Because the apply phase stops holding the
store once the wall hits, the follow-up inspection can still read the O(1)
page metadata instead of reporting it unavailable.

## Two doctor modes and check scoping

| Mode | When | Store access | `hook-spool` unit |
|---|---|---|---|
| **Full** | store file missing, or smaller than 2 GiB | opens SQLite for store-scoped checks | **decoded records** for the selected `--client` filter, plus `filesystem pending files (store-independent)=N` so the number can be compared with a metadata-only run |
| **Metadata-only** | regular store file ≥ 2 GiB (`mode: "metadata_only_large_store"`) | filesystem metadata plus the O(1) `mode=ro` page/projection read; `--fix` may additionally open SQLite for hook-spool replay only | **files** labeled `metadata-only, store-independent` (`pending=` is a directory entry count, not decoded records) |

Store-independent checks (hook spool, hook-state residue, plugin cache, `path` / `config`) say `store-independent` on the output line. They inspect host files, not the SQLite store. SessionEnd cancellation markers are resolved against the store via a bounded `mode=ro` sessions primary-key lookup (no event bodies or dbstat), so they are no longer store-independent-only. Store-scoped checks (capacity, memory activation, projection) inspect the store at `DB_PATH`.

When doctor ran with `--db-path` or `TRACEARY_DB_PATH`, store-addressed hint commands (`traceary doctor`, `traceary store`, `traceary memory`) include `--db-path` so executing them verbatim hits the same store. Host-only commands (`claude plugin update`, `which -a traceary`) are left unchanged. The default home store does not inject `--db-path`.
