# Traceary

[日本語](./README.ja.md)

[![CI](https://github.com/duck8823/traceary/actions/workflows/ci.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/ci.yml)
[![Release](https://github.com/duck8823/traceary/actions/workflows/release.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/release.yml)

[Changelog](./CHANGELOG.md)

[Documentation guide](./docs/README.md)

[Contributing](./CONTRIBUTING.md)

[Security policy](./SECURITY.md)

[MCP guide](./docs/mcp/README.md)

[Backup guide](./docs/backup/README.md)

[Storage model](./docs/storage/README.md)

[Release guide](./docs/release/README.md)

[CLI reference](./docs/cli/README.md)

[Environment reference](./docs/environment/README.md)

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

## Install

### go install

```sh
go install github.com/duck8823/traceary@latest
```

### Homebrew

After a tagged release updates `Formula/traceary.rb`, macOS users can install Traceary with Homebrew:

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### Prebuilt binaries

Tagged releases publish macOS and Linux archives on GitHub Releases.
See [`docs/release/README.md`](./docs/release/README.md) for the release flow and local snapshot builds.

## Platform support

- prebuilt release archives are published for macOS and Linux
- the core CLI and `traceary mcp-server` are actively tested on macOS and Linux
- other Go-supported Unix-like environments may work via `go install`, but they are not part of the current support promise
- hooks currently target bash-based Unix-like environments; native Windows PowerShell / `cmd.exe` workflows are not supported today
- if you need Windows today, use WSL or another POSIX-compatible environment

## CLI language

- default operator-facing help, success messages, and common errors use English
- set `TRACEARY_LANG=ja` when you want Japanese CLI messaging instead
- `--json` output is language-neutral

## Privacy and telemetry

- Traceary is local-first: it writes to a local SQLite file and does not require a hosted Traceary service
- the project does not include built-in telemetry, analytics, or crash reporting
- your data leaves the machine only when you explicitly copy the SQLite file, publish a backup, or wire Traceary into another tool yourself

See [`docs/storage/README.md`](./docs/storage/README.md) for the storage model and [`SECURITY.md`](./SECURITY.md) for the security disclosure path.

## Support expectations

- Traceary is maintained as an actively evolving OSS tool, not a managed service with an SLA
- bug reports and improvement requests belong in GitHub Issues
- changes can still happen quickly within `v0.x`, so read the changelog before upgrading automation around it

## Quick start

`traceary init` is optional. Every command creates the SQLite DB and applies migrations on demand. Use `init` only when you want to bootstrap the DB path explicitly or verify write permissions before a session starts.

### First successful trace

This is the shortest loop that proves the DB, session recording, and event lookup all work.

```sh
sid=$(traceary session start --client dogfood --agent codex)
event_id=$(traceary log --client dogfood --agent codex --session-id "$sid" --id-only "Investigating failing tests")
traceary show "$event_id" --json
```

### Add command output to the same session

After the first note works, add a command audit and search it back.

```sh
event_id=$(
  traceary audit \
    --client dogfood \
    --agent codex \
    --session-id "$sid" \
    --id-only \
    --command "go test ./..." \
    --input '{"stdin":""}' \
    --output '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}'
)
traceary search boom --json
traceary show "$event_id" --json
traceary session active
```

Example output:

```text
$ traceary init
Initialized: /Users/you/.config/traceary/traceary.db

$ traceary session start --client dogfood --agent codex
session-1ceee1eaa50a31687cfdb2c8a6fcc85d

$ traceary audit --id-only ...
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
- `traceary session start` already prints a script-friendly session ID, so shell wrappers do not need extra parsing
- use `docs/cli/README.md` when you want the full command reference instead of the first-run path

What changes after this:

- you can search past command output by text
- you can recover the current session ID without manually tracking it
- you can inspect one event in detail and hand it to another AI tool
- large audit payloads are truncated before they grow the DB too much

## Core commands

Current core commands:

```sh
traceary init
traceary log <message>
traceary audit [<command> <input> <output>]
traceary search <query>
traceary list
traceary context
traceary handoff
traceary session start
traceary session end
traceary session latest
traceary session active
traceary show <event-id>
traceary doctor
traceary backup create --output <path>
traceary backup restore --input <path>
traceary hooks print --client <claude|codex|gemini>
traceary hooks install --client <claude|codex|gemini>
traceary hooks guide --client <claude|codex|gemini>
traceary mcp-server
traceary gc
```

Use `--id-only` with mutating commands when a shell script wants the resulting identifier without parsing human-readable text.

```sh
traceary log --id-only "Investigating failing tests"
traceary audit --id-only --command "go test ./..." --input '{}' --output '{}'
traceary session end --session-id "$sid" --id-only
```

When `traceary log` or `traceary audit` omit `--session-id`, Traceary first tries the latest non-stale active session for the resolved repo/work context. If none matches, it falls back to the historical `default` session ID and prints a notice in human-readable mode.

Useful `search --kind` values:

```sh
traceary search --kind command_executed
traceary search --kind note
traceary search --kind session_started
```

- valid values: `note`, `command_executed`, `reviewed`, `session_started`, `session_ended`
- alias: `audit` = `command_executed`

`list` and `search` use stable offset pagination with `ORDER BY created_at DESC, event_id DESC`.
Use the same filters with a larger `--offset` when you want the next page.

```sh
traceary list --limit 20 --offset 20
traceary search boom --limit 20 --offset 40 --json
```

`traceary session active` defaults to `--stale-after 24h`; pass `--allow-stale` to inspect an older unclosed session.

`traceary session start` prints the session ID so scripts can capture it immediately. `traceary session end` prints the recorded event ID because the caller already knows which session it is closing.

Hook setup: [`docs/hooks/README.md`](./docs/hooks/README.md)

Full CLI reference: [`docs/cli/README.md`](./docs/cli/README.md)

Environment variables and runtime assumptions: [`docs/environment/README.md`](./docs/environment/README.md)

MCP integration: [`docs/mcp/README.md`](./docs/mcp/README.md)

Backup and machine transfer: [`docs/backup/README.md`](./docs/backup/README.md)

Storage model, schema, and GC defaults: [`docs/storage/README.md`](./docs/storage/README.md)

All commands resolve the SQLite path in this order: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`.

`traceary audit` keeps input/output at `64 KiB` each by default. Use `--max-input-bytes`, `--max-output-bytes`, or `TRACEARY_MAX_AUDIT_INPUT_BYTES` / `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` when you want a stricter limit. The CLI prints a notice when truncation happens.

`traceary audit` also redacts common secret-like values before they reach SQLite (for example `Authorization:` headers, `TOKEN=...`, JSON `access_token`, and private key blocks). This is a best-effort safeguard, not a complete DLP system. Use `--allow-secrets` or `TRACEARY_ALLOW_SECRETS=true` only when you intentionally want raw payload persistence.

CLI failures are printed to stderr as plain `Error: ...` lines so hooks and shell scripts can parse them without JSON log noise.

## License

MIT. See [`LICENSE`](./LICENSE).

## Non-goals

- Semantic search / embeddings
- Team sharing or cloud sync
- Web UI / dashboard
- Enterprise audit / RBAC
- Full file-state snapshot reproduction
- Live observability
