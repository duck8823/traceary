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
- Codex CLI: no host session-end signal — `Stop` is a per-response turn boundary (#1170), so a Codex session ends via MCP `manage_session` or activity-aware stale GC (automatic after normal hook starts, with `traceary session gc` available manually)
- Gemini CLI: `SessionEnd` is also treated as best-effort

If session-end fidelity matters, prefer the client integrations that expose an explicit end hook.

### Hook state attaches to the wrong session

This usually means the default PPID-based grouping does not match your local client process topology.
Set `TRACEARY_HOOK_STATE_KEY` explicitly in the hook environment when you need a more stable grouping key.

### Audit reliability dogfood signal

`traceary doctor` includes an `audit-reliability` check over a bounded recent command-audit window. It reports duplicate command-audit candidate groups and workspace-drift candidates when `cwd` evidence in the stored audit input conflicts with the event workspace metadata.

Treat this as a dogfood review signal, not as automatic cleanup. The check intentionally prints counts and sampled event IDs only; it does not dump command input/output bodies. Before using process-review metrics, inspect the sampled rows with `traceary show <event_id>` and confirm whether they are real duplicates/drift or legitimate repeated work.

### Content event reliability dogfood signal

`traceary doctor` also includes a `content-event-reliability` check over a bounded recent prompt/transcript hook window. It reports duplicate content candidate groups when the same captured prompt or transcript content is recorded more than once within that window.

Treat this as a dogfood review signal, not as automatic cleanup. The check intentionally prints counts and sampled event IDs only; it does not dump captured content bodies. Before acting on the metric, inspect the sampled rows with `traceary show <event_id>` and confirm whether they are real duplicates or legitimate repeated content.

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

## Workspace identity release QA

Run `traceary doctor` once to initialize/migrate the store, then run `traceary report workspace-identity` to inspect attribution coverage, current workspace relationships, and stable-ID hook delivery outcomes by client and hook. The report itself does not migrate or advance provenance catch-up. `--json` is suitable for release QA. Conflict samples contain identifiers only; event bodies are never included.

The default report reads only body-free exact-delivery and workspace projections. It deliberately separates `exact_delivery` (proven stable host-ID outcomes, with a target below 1%) from `heuristic_candidates`, whose `measurement_state` is `not_requested` unless the operator passes `--include-heuristic`. Explicit heuristic measurement reads at most `--heuristic-limit` prompt/transcript bodies (default 5,000) and reports `partial`, `complete`, or `failed`; it never changes the exact metrics. `sample_available=false` means no runtime delivery attempts have been measured yet.

Reviewed conflicts can be reclassified without changing canonical session provenance:

```sh
traceary store workspace-alias add --session <id> --workspace <path> --reviewed-by <operator> --note <reason>
traceary store workspace-alias list
traceary store workspace-alias remove --session <id> --workspace <path>
```

Aliases affect the current diagnostic projection only. Removing one restores the original relationship classification.

## Related docs

- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- storage model: [`../storage/README.md`](../storage/README.md)
- backup guide: [`../backup/README.md`](../backup/README.md)
- Scheduled operations: [`./scheduled-tasks.md`](./scheduled-tasks.md)
- Python dependency plan: [`./python-dependencies.md`](./python-dependencies.md)
- Repository tooling plan: [`./repo-tooling.md`](./repo-tooling.md)
- Memory command surface plan: [`./memory-command-surface.md`](./memory-command-surface.md)
- Workspace identity contract: [`./workspace-identity-contract.md`](./workspace-identity-contract.md)
- Evidence-backed one-shot repair: [`./one-shot-repair.md`](./one-shot-repair.md)
