# Traceary

[日本語](./README.ja.md)

Traceary is a CLI and MCP server for recording and searching AI agent work logs and audit logs locally.

Traceary is a local-first log store for AI coding agents.
It records session notes and tool execution history into SQLite,
and lets Claude Code / Codex / Gemini read the same context from CLI or MCP.

## Why

Using AI agents in daily development creates a few persistent problems.

- Session context is easy to lose after `clear` / `compact`
- Git history can show what changed, but not always why it changed
- It is hard to see which agent ran which command
- Context gets fragmented across Claude / Codex / Gemini
- Parallel sessions and worktree moves make history harder to follow
- Log data keeps growing, so retention and GC matter

Traceary aims to keep session logs and audit logs in one local store,
so multiple AI tools can read and write the same history.

## Features

- Record session logs and audit logs in SQLite
- Search logs by text and date range
- Share context across Claude Code / Codex / Gemini via MCP
- Identify repositories by git remote URL to survive worktree moves
- Isolate records by `session_id` and keep attribution with `client` / `agent`
- Control database size with retention and `gc`

## How it works

### CLI

Planned commands for v0.1:

```sh
traceary log <message>
traceary audit <command> <input> <output>
traceary search <query>
traceary list
traceary session start
traceary session end
traceary gc
```

### MCP server

Traceary will expose the following tools over stdio transport:

- `add_log`
- `add_audit`
- `search`
- `get_context`

This allows Claude Code / Codex / Gemini to read and write the same local log store.

### Storage

- SQLite (`~/.config/traceary/traceary.db`)
- local-first, no external API required
- repository association by git remote URL
- database size limit and log rotation for long-term use

### Data model

Each record keeps the metadata needed for multi-agent operation.

- `client` (`claude-code` / `codex` / `gemini`)
- `agent` (`reviewer`, `planner`, etc.)
- `session_id`
- repository identity derived from git remote URL

## Hooks / Integration

For v0.1, Traceary is intended to be connected from AI agent runtimes as follows.

- Session start / end hooks record session boundaries
- Post-tool hooks record Bash command audit logs
- Recent failure logs can be injected at session start to reduce retry loops

## Scope

### v0.1

- Single binary implementation in Go
- SQLite-based local storage
- CLI for logging, audit, search, listing, session boundaries, and GC
- MCP server for shared access across AI tools
- Retention policy for keeping the database manageable

### Out of scope

- Semantic search / embeddings
- Team sharing or cloud sync
- Web UI / dashboard
- Enterprise audit / RBAC
- Full file-state snapshot reproduction
- Live observability
