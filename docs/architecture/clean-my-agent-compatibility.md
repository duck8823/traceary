# Clean My Agent relay compatibility: evaluation and decision

[日本語](./clean-my-agent-compatibility.ja.md)

This document evaluates whether Traceary should align its session/event/bundle/replay surfaces with [Clean My Agent](https://github.com/blain3white/clean-my-agent)'s universal relay schema, and records the decision. The reference surfaced when the Clean My Agent maintainer linked the project from #1170; this note keeps the implementation issues (#1170, #1171, #1172, #1173, #1169) focused by capturing the comparison in one bounded place.

**TL;DR**: **reference-only**. Traceary documents the mapping below and uses Clean My Agent's storage-format documentation as external ground truth for host-side session storage (regression fixtures in #1170, diagnosis in #1171/#1174), but does **not** export `clean-my-agent.universal-session.v1` in v0.21.0. The decision is revisited when the upstream schema stabilizes into a standalone versioned spec.

## What Clean My Agent is

Clean My Agent is a local-first desktop app (MIT license; latest checked release `v0.1.3`, published 2026-06-09) that scans Codex, Claude Code, Cursor, Gemini, and OpenCode session data and provides usage analytics, backups, exports, cleanup suggestions, an app-managed Trash, and restore.

Two of its artifacts are relevant to Traceary:

1. **`docs/agent-storage-formats.md`** — schema-only documentation of host session storage roots and record shapes. It explicitly avoids copying prompt text, tool output, auth data, or user file contents.
2. **`UniversalRelayDocument`** (`src/shared/types.ts`) — an export schema identified as `clean-my-agent.universal-session.v1`, produced by the app's relay/export feature.

Two accuracy notes from primary-source verification (2026-06-10):

- The universal relay schema is **not** defined in `docs/agent-storage-formats.md`; it lives only in the app's TypeScript types and the relay view component. There is no standalone JSON Schema or compatibility policy yet.
- `docs/agent-storage-formats.md` documents Codex, Claude Code, Cursor, and OpenCode, but **has no Gemini section**. The Gemini storage roots referenced from #1171 (`~/.gemini`, `~/.config/gemini`, `~/Library/Application Support/Gemini`) come from the app's Gemini scanner provider code, not from that document.

## The universal relay schema (as of v0.1.3)

`UniversalRelayDocument` fields, verbatim from `src/shared/types.ts`:

| Field | Shape |
| --- | --- |
| `schema` | `"clean-my-agent.universal-session.v1"` |
| `exportedAt` | timestamp |
| `source` | agent identifier (e.g. `codex`) |
| `session` | `SessionRecord`: `id`, `source`, `title`, `projectName`, `projectPath`, `branch`, `storagePath`, `storageKind` (`file`/`directory`/`database`), `storageState`, `createdAt`, `lastUpdated`, `messageCount`, `tokens`, `sizeBytes`, `backupStatus`, `tags`, `searchText`, `metadata` |
| `messages` | array of `UniversalRelayMessage`: `id`, `role` (`system`/`user`/`assistant`/`tool`/`unknown`), `createdAt`, `text`, `raw` |
| `files` | array of `{path, reason, lastSeenAt}` |
| `commands` | array of `{command, cwd, createdAt}` |
| `git` | `{branch, projectPath, diff}` |
| `attachments` | array of `{path, mediaType, sizeBytes}` |
| `warnings` | array of strings |

## Mapping: Traceary surfaces to relay concepts

| Traceary surface | Relay concept | Fit | Notes |
| --- | --- | --- | --- |
| `Session` (`session_id`, `started_at`, `ended_at`, `runtime_mode`, `terminal_reason`, `client`, `agent`, `workspace`, `label`, `summary`) | `session` (`id`, `createdAt`, `lastUpdated`, `source`, `projectPath`/`projectName`, `title`) | Partial | `ended_at`, `runtime_mode`, and `terminal_reason` have no relay slots (`lastUpdated` is the closest to an end timestamp), and `summary` has none either (`session.metadata` at best). Relay's `tokens`, `sizeBytes`, `storageState`, `backupStatus` have no Traceary source of truth. |
| `Session` subagent lineage (`parent_session_id`, `spawn_event_id`, `subagent_kind`, `spawn_order`) | none | None | Relay documents are flat per-session exports. Traceary's session tree (parent/child spawn lineage) has no relay representation, so an export would sever subagent sessions from their parents. |
| `Event` `kind=prompt` | `messages[]` with `role=user` | Good | Traceary can fill `id`, `createdAt`, `text`. |
| `Event` `kind=transcript` | `messages[]` with `role=assistant` | Good | Same as above. |
| `Event` `kind=compact_summary` | `warnings[]` (or `messages[]` with `role=system`) | Lossy | Compaction boundaries are a Traceary lifecycle concept; relay has no equivalent. |
| `Event` `kind=note` / `reviewed` / `session_started` / `session_ended` | none (`warnings[]` at best) | None | Lifecycle and review events have no relay home. |
| `CommandAudit` / `Event` `kind=command_executed` | `commands[]` (`{command, cwd, createdAt}`) | Lossy | Relay drops Traceary's richest audit data: `exit_code`, `failed`, `input`, `output`, truncation/redaction flags. `cwd` is only approximated by `workspace` when it is a local path. |
| Workspace metadata (`workspace`) | `git.projectPath` / `session.projectPath` | Partial | Maps when the workspace is a filesystem path; remote-URL workspaces have no relay slot. |
| Branch metadata | `git.branch` / `session.branch` | Gap | Traceary does not persist a git branch. Host payloads contain it (e.g. Codex `session_meta.payload.git.branch`), but capturing it is out of scope for v0.21.0. |
| Body truncation/redaction metadata (`input_truncated`, `output_truncated`, `input_redacted`, `output_redacted`; extended by #1173) | `warnings[]` | Good (as precedent) | Relay's `warnings` is a useful precedent: ingest-time truncation should be visible to any downstream consumer, not silent. |
| Durable memory (`Memory`: type/scope/status/confidence) | none | None | Relay is a per-session conversation export; durable memory is cross-session knowledge. Different jobs. |
| Bundle export (`manifest_version=2` tar.gz of NDJSON tables) | none | None | Traceary's bundle is a whole-store backup/transfer format with conflict policies, not a per-session relay document. |
| Replay (`traceary replay` HTML) | none | None | Replay is an operator-facing review view, not a machine-readable interchange format. |

## Decision

**Reference the schema; do not export it in v0.21.0.**

Rationale:

1. **Schema maturity.** The schema exists only inline in a `v0.1.x` application's TypeScript types. There is no standalone versioned spec, no JSON Schema, and no documented compatibility policy. Targeting it now means chasing churn.
2. **Surface discipline.** Traceary's MCP tool count is frozen and the CLI surface is deliberately strict. A third export surface (next to bundle and replay) needs a stronger justification than "a schema exists".
3. **Information shape mismatch.** Half of the relay document would be empty (`branch`, `tokens`, `sizeBytes`, `files`, `attachments` have no Traceary source), and the half Traceary is richest in (command audits with exit codes, failure flags, truncation metadata) would be flattened to `{command, cwd, createdAt}`.
4. **No consumer demand.** No tool currently consumes `universal-session.v1` documents from third parties.

Revisit triggers (any of):

- upstream publishes the schema as a standalone versioned spec (JSON Schema or equivalent) with a compatibility policy
- a second producer or consumer of the format appears
- Traceary starts persisting branch metadata, closing the largest mapping gap
- a concrete operator workflow needs to hand a Traceary session to another tool in a neutral format

If a compatible export is implemented later, it must not add an MCP tool (the tool surface is frozen) and should be designed as an explicit additive CLI surface at that time — this note deliberately does not pre-commit a command shape.

## What v0.21.0 adopts from the reference

- **Host storage ground truth for fixtures and diagnosis.** Codex roots (`~/.codex/sessions/YYYY/MM/DD/*.jsonl`, `~/.codex/archived_sessions/*.jsonl`, `~/.codex/session_index.jsonl`; record union of `session_meta` / `response_item` / `event_msg` / `turn_context`) ground the #1170 regression fixtures. Claude Code roots (`~/.claude/projects/<encoded-project-path>/*.jsonl`, `~/.claude/transcripts/*.jsonl`) support the #1174 coverage-gap diagnosis. Gemini scanner roots support the #1171 root-cause comparison.
- **Fixture policy.** Fixtures stay schema-shaped only: no real prompt text, tool output, credentials, or user file contents. This matches both Clean My Agent's documentation approach and the acceptance criteria on #1170/#1171.
- **Safety semantics as a reference for memory hygiene (#1169).** Clean My Agent's model — read-before-write, explicit cleanup suggestions, backup before risky cleanup, app-managed Trash/restore — maps to Traceary's dry-run-first cleanup and evidence-first review. Traceary keeps its existing stance: no bulk accept, no destructive cleanup added by this comparison.
- **Truncation visibility precedent (#1173).** Relay surfaces conversion caveats in `warnings`; Traceary's ingest-time truncation metadata should likewise be visible in CLI/MCP output rather than silent.
- **Session liveness vocabulary check (#1172).** Relay's `session.storageState` shows that downstream consumers want an explicit liveness/state field. The #1170/#1172 status vocabulary (e.g. late events after an end) should stay explicit for the same reason.

## Non-goals (unchanged from #1177)

- Do not vendor Clean My Agent.
- Do not implement a desktop cleanup UI.
- Do not add destructive cleanup behavior.
- Do not import real local agent logs into tests.

## Sources

- Repository: <https://github.com/blain3white/clean-my-agent> (MIT, `v0.1.3` released 2026-06-09; verified 2026-06-10)
- `docs/agent-storage-formats.md` (storage roots and record shapes)
- `src/shared/types.ts` (`UniversalRelayDocument`, `SessionRecord`, `UniversalRelayMessage`, `ExportFormat`)
- `src/features/relay/RelayView.tsx` (relay/export feature)

## Related docs

- [Architecture principles](./README.md)
- [Event lifecycle](../lifecycle.md)
- [Hook contract](../hooks/contract.md)
- [Backup guide](../backup/README.md)
- Implementation issues: #1170, #1171, #1172, #1173, #1169
