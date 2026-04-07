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

## Quick start

If you only want to see what Traceary changes in daily work, this is the shortest loop.

```sh
traceary init
sid=$(traceary session start --client dogfood --agent codex)
traceary log --client dogfood --agent codex --session-id "$sid" "Investigating failing tests"
event_id=$(
  traceary audit \
    --client dogfood \
    --agent codex \
    --session-id "$sid" \
    "go test ./..." \
    '{"stdin":""}' \
    '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}' |
    awk '{print $2}'
)
traceary search boom --json
traceary show "$event_id" --json
traceary session active
```

Example output:

```text
$ traceary init
初期化しました: /Users/you/.config/traceary/traceary.db

$ traceary session start --client dogfood --agent codex
session-1ceee1eaa50a31687cfdb2c8a6fcc85d

$ traceary audit ... | awk '{print $2}'
0dc6d0c579df5e539c27df56e131570a

$ traceary search boom --json
[
  {
    "event_id": "0dc6d0c579df5e539c27df56e131570a",
    "kind": "command_executed",
    "message": "go test ./..."
  }
]

$ traceary show 0dc6d0c579df5e539c27df56e131570a --json
{
  "event": {
    "kind": "command_executed",
    "message": "go test ./..."
  },
  "command_audit": {
    "output": "{\"stdout\":\"panic: boom\",\"stderr\":\"stacktrace\",\"exitCode\":1}"
  }
}

$ traceary session active
session-1ceee1eaa50a31687cfdb2c8a6fcc85d
```

Notes:

- `traceary session active` treats sessions older than `24h` as stale by default
- use `traceary session active --allow-stale` when you need to inspect an old unclosed session

What changes after this:

- you can search past command output by text
- you can recover the current session ID without manually tracking it
- you can inspect one event in detail and hand it to another AI tool
- large audit payloads are truncated before they grow the DB too much

## Core commands

Current core commands:

```sh
traceary log <message>
traceary audit <command> <input> <output>
traceary search <query>
traceary list
traceary session start
traceary session end
traceary session latest
traceary session active
traceary show <event-id>
traceary gc
```

Useful `search --kind` values:

```sh
traceary search --kind command_executed
traceary search --kind note
traceary search --kind session_started
```

- valid values: `note`, `command_executed`, `reviewed`, `session_started`, `session_ended`
- alias: `audit` = `command_executed`

`traceary session active` defaults to `--stale-after 24h`; pass `--allow-stale` to inspect an older unclosed session.

Hook setup: [`docs/hooks/README.md`](./docs/hooks/README.md)

All commands resolve the SQLite path in this order: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`.

`traceary audit` keeps input/output at `64 KiB` each by default. Use `--max-input-bytes`, `--max-output-bytes`, or `TRACEARY_MAX_AUDIT_INPUT_BYTES` / `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` when you want a stricter limit. The CLI prints a notice when truncation happens.

## Non-goals

- Semantic search / embeddings
- Team sharing or cloud sync
- Web UI / dashboard
- Enterprise audit / RBAC
- Full file-state snapshot reproduction
- Live observability
