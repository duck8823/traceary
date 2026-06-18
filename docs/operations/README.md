# Operational assumptions

[日本語](./README.ja.md)

This guide documents the practical runtime assumptions behind Traceary's local SQLite store and generated hook scripts.
It is intentionally candid about what Traceary assumes today, what is merely best-effort, and where users may still need manual overrides.

## SQLite concurrency model

Traceary relies on SQLite itself for cross-process coordination.

Current assumptions:

- normal usage is many short-lived CLI, hook, or MCP processes sharing one DB file
- writes are small append-style operations (`events`, `command_audits`, session boundaries)
- SQLite serializes those writes safely at the file level

Connection settings applied by Traceary:

- `journal_mode=WAL` — readers (e.g. `traceary tail` polling) and writers can proceed concurrently without blocking each other
- `synchronous=NORMAL` — the recommended durability setting for WAL mode; fsyncs occur at checkpoint boundaries
- `busy_timeout=5000` — SQLite auto-retries on transient lock contention for up to 5 seconds before surfacing `SQLITE_BUSY`
- `foreign_keys=1` — foreign-key constraints are enforced

WAL mode produces two sidecar files next to the main database: `<db>-wal` and `<db>-shm`. Backup and restore already handle these files; manual copies of the DB should include them for a consistent snapshot.

What Traceary does **not** add today:

- no custom background writer or queue
- no application-level retry loop beyond SQLite's `busy_timeout`
- no distributed or multi-host coordination

Practical guidance:

- a few parallel AI sessions on one machine are in scope
- extremely high write volume to the same DB path is not a tuned use case today
- if you still hit `SQLITE_BUSY` past the 5-second busy window, reduce concurrent writers or split DB paths by workflow

## Hook session-state assumptions

The generated hook scripts persist the last resolved session ID in a small local state file.

By default the state key is based on:

- client name
- `TRACEARY_HOOK_STATE_KEY`, when explicitly set
- otherwise the current `PPID` (falling back to `$$` when needed)

This means the current hook design assumes:

- related hook invocations for one interactive client session usually share a stable parent process identity
- the client keeps invoking hooks from a process tree that makes `PPID` a useful grouping key

This is intentionally best-effort, not a protocol guarantee.
If a client changes its process model, or if your wrapper scripts create a different parent/child layout, override the grouping with `TRACEARY_HOOK_STATE_KEY`.

## Known failure modes

### `doctor` passes but hooks still do not fire

`traceary doctor` validates DB access, hook-script materialization, and whether Traceary-managed entries exist in the target config file.
It does **not** launch the third-party client or prove that every hook event fires in exactly the same way as the local validated build.

### Session end is best-effort for some clients

- Claude Code: dedicated `SessionEnd` is supported in the documented integration
- Codex CLI: no host session-end signal — `Stop` is a per-response turn boundary (#1170), so a Codex session ends only via MCP `manage_session` or stale GC (`traceary session gc`)
- Gemini CLI: `SessionEnd` is also treated as best-effort

If session-end fidelity matters, prefer the client integrations that expose an explicit end hook.

### Hook state attaches to the wrong session

This usually means the default PPID-based grouping does not match your local client process topology.
Set `TRACEARY_HOOK_STATE_KEY` explicitly in the hook environment when you need a more stable grouping key.

### Audit reliability dogfood signal

`traceary doctor` includes an `audit-reliability` check over a bounded recent command-audit window. It reports duplicate command-audit candidate groups and workspace-drift candidates when `cwd` evidence in the stored audit input conflicts with the event workspace metadata.

Treat this as a dogfood review signal, not as automatic cleanup. The check intentionally prints counts and sampled event IDs only; it does not dump command input/output bodies. Before using process-review metrics, inspect the sampled rows with `traceary show <event_id>` and confirm whether they are real duplicates/drift or legitimate repeated work.

### Concurrent cleanup versus active ingestion

`traceary store gc` deletes old rows and then runs `VACUUM`.
Do not treat aggressive cleanup as a background maintenance task while many sessions are actively writing to the same DB.
Take a backup first and prefer running cleanup during a quieter period.

## What is supported versus merely possible

Supported today:

- one machine
- one local SQLite file per workflow or project group
- several concurrent human/AI sessions with modest write volume

Possible but not actively tuned:

- many high-frequency parallel writers to a single DB
- client wrappers that radically change hook process topology
- non-POSIX hook environments

## Recommended mitigations

1. use `traceary doctor` before blaming hook generation
2. set `TRACEARY_DB_PATH` explicitly when multiple workflows should not share one DB
3. set `TRACEARY_HOOK_STATE_KEY` when PPID-based grouping is not stable enough
4. run `traceary store backup create` before risky cleanup or manual inspection
5. document client-specific caveats in your own team automation if you rely on best-effort session-end hooks

## Related docs

- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- storage model: [`../storage/README.md`](../storage/README.md)
- backup guide: [`../backup/README.md`](../backup/README.md)
- Scheduled operations: [`./scheduled-tasks.md`](./scheduled-tasks.md)
- Python dependency plan: [`./python-dependencies.md`](./python-dependencies.md)
- Repository tooling plan: [`./repo-tooling.md`](./repo-tooling.md)
- Memory command surface plan: [`./memory-command-surface.md`](./memory-command-surface.md)
