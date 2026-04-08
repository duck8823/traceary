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

What Traceary does **not** add today:

- no custom background writer or queue
- no application-level retry loop for sustained `database is locked` pressure
- no distributed or multi-host coordination

Practical guidance:

- a few parallel AI sessions on one machine are in scope
- extremely high write volume to the same DB path is not a tuned use case today
- if you repeatedly hit SQLite lock errors, reduce concurrent writers, split DB paths by workflow, or retry the operation after the short-lived competing process exits

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
- Codex CLI: stop/session-end capture is best-effort while the local build only exposes `Stop`
- Gemini CLI: `SessionEnd` is also treated as best-effort

If session-end fidelity matters, prefer the client integrations that expose an explicit end hook.

### Hook state attaches to the wrong session

This usually means the default PPID-based grouping does not match your local client process topology.
Set `TRACEARY_HOOK_STATE_KEY` explicitly in the hook environment when you need a more stable grouping key.

### Concurrent cleanup versus active ingestion

`traceary gc` deletes old rows and then runs `VACUUM`.
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
4. run `traceary backup create` before risky cleanup or manual inspection
5. document client-specific caveats in your own team automation if you rely on best-effort session-end hooks

## Related docs

- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- storage model: [`../storage/README.md`](../storage/README.md)
- backup guide: [`../backup/README.md`](../backup/README.md)

