# Traceary

[日本語](./README.ja.md)

Traceary is a local-first CLI and MCP server for recording and searching AI agent work logs and audit logs.

## Why

Using AI agents in daily development creates a few persistent problems.

- Session context is easy to lose after `clear` / `compact`
- Git history can show what changed, but not always why it changed
- It is hard to see which agent ran which command
- Context gets fragmented across Claude / Codex / Gemini
- Parallel sessions and worktree moves make history harder to follow
- Log data keeps growing, so retention and `gc` matter

Traceary keeps session logs and audit logs in one local store,
so multiple AI tools can read and write the same history.

## Features

- Store session logs and audit logs locally in SQLite
- Search logs by text and date range
- Share context across Claude Code / Codex / Gemini via MCP
- Ingest session boundaries and shell audits from Claude Code / Codex / Gemini hooks
- Associate records with repositories by git remote URL
- Keep attribution with `client`, `agent`, and `session_id`
- Manage long-term data growth with retention and `gc`

## Planned commands

For v0.1:

```sh
traceary log <message>
traceary audit <command> <input> <output>
traceary search <query>
traceary list
traceary session start
traceary session end
traceary gc
```

Hook setup: [`docs/hooks/README.md`](./docs/hooks/README.md)

## Non-goals

- Semantic search / embeddings
- Team sharing or cloud sync
- Web UI / dashboard
- Enterprise audit / RBAC
- Full file-state snapshot reproduction
- Live observability
