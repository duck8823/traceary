# Traceary

[日本語](./README.ja.md)

<p align="center">
  <img src="./docs/assets/traceary-mark.svg" alt="Traceary mark" width="120">
</p>

[![CI](https://github.com/duck8823/traceary/actions/workflows/ci.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/ci.yml)
[![Release](https://github.com/duck8823/traceary/actions/workflows/release.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/release.yml)

Traceary is a local-first CLI and MCP server for recording and searching AI agent work logs, session boundaries, and shell command audits.

If your goal is automatic recording, start with the host integration instead of the raw CLI.

## Why Traceary

AI-assisted development gets messy quickly when:

- session context disappears after `clear` or `compact`
- Git history explains what changed, but not always why
- shell command output is hard to connect back to the right agent or session
- work is split across Claude, Codex, Gemini, and manual terminal steps
- multiple sessions and worktree moves make the timeline harder to follow

Traceary keeps those records in one local SQLite store so the same history can be reused from the CLI, hooks, and MCP clients.

## What it stores

- notes and review records
- session start/end events
- shell command audits
- attribution such as `client`, `agent`, `session_id`, and repository/work context

Traceary is local-first. It writes to SQLite on your machine and does not include built-in telemetry, analytics, or hosted storage.

## Install into your agent host

These are the fastest paths when you want Traceary to record sessions and shell commands automatically.

| Host | Install path | Guide |
| --- | --- | --- |
| Claude Code | `claude plugins marketplace add https://github.com/duck8823/traceary` then `claude plugins install traceary@traceary-plugins --scope user` | [Claude Code plugin](./docs/integrations/claude-plugin.md) |
| Codex | `git clone https://github.com/duck8823/traceary ~/src/traceary` then `python3 ~/src/traceary/scripts/codex/install_plugin.py` | [Codex plugin](./docs/integrations/codex-plugin.md) |
| Gemini CLI | `gemini extensions install https://github.com/duck8823/traceary --ref <tag>` | [Gemini CLI extension](./docs/integrations/gemini-extension.md) |

For the integration overview, use the [native integrations guide](./docs/integrations/README.md).

## Install the Traceary CLI

### go install

```sh
go install github.com/duck8823/traceary@latest
```

### Homebrew

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### Prebuilt binaries

Tagged releases publish macOS and Linux archives on GitHub Releases.
See the [release guide](./docs/release/README.md) when you want the packaging details.

## Quick start

`traceary init` is optional. Commands create the database and run migrations on demand.
Use `init` only when you want to create the DB path up front or confirm write permissions before a session starts.

### 1. Start a session and write a note

```sh
sid=$(traceary session start --client dogfood --agent codex)
event_id=$(traceary log --client dogfood --agent codex --session-id "$sid" --id-only "Investigating failing tests")
traceary show "$event_id" --json
```

### 2. Record command output in the same session

```sh
traceary audit \
  --client dogfood \
  --agent codex \
  --session-id "$sid" \
  --command "go test ./..." \
  --input '{"stdin":""}' \
  --output '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}'

traceary search boom --json
traceary session active
```

### 3. Use script-friendly output when needed

```sh
traceary log --id-only "Investigating failing tests"
traceary audit --id-only --command "go test ./..." --input '{}' --output '{}'
traceary session end --session-id "$sid" --id-only
```

## Manual CLI workflows

If you need one-off/manual usage outside the host integrations, the usual entry points are:

- `traceary session start`
- `traceary log`
- `traceary audit`
- `traceary list` / `traceary search`
- `traceary doctor`

Use the [CLI reference](./docs/cli/README.md) for the full command surface.

## Defaults worth knowing

- `traceary log` and `traceary audit` reuse the latest non-stale active session for the resolved repo/work context when `--session-id` is omitted; when `remote.origin.url` is missing inside a git worktree, Traceary falls back to the worktree root path
- `traceary session active` treats sessions older than `24h` as stale unless you pass `--allow-stale`
- `traceary session start` prints a session ID; `traceary session end` prints the recorded event ID
- default operator-facing CLI output is English; set `TRACEARY_LANG=ja` when you want Japanese messaging
- `--json` output stays language-neutral

## Documentation

Use the [documentation index](./docs/README.md) for the full map.
The most common next pages are:

- [Native integrations](./docs/integrations/README.md)
- [CLI reference](./docs/cli/README.md)
- [Hooks guide](./docs/hooks/README.md)
- [MCP guide](./docs/mcp/README.md)
- [Environment and storage notes](./docs/environment/README.md)

## Contributing and support

- bug reports and feature requests belong in GitHub Issues
- security reports should follow the private contact path in [CONTRIBUTING.md](./CONTRIBUTING.md)
- this is an actively evolving `v0.x` OSS tool, so check the [changelog](./CHANGELOG.md) before upgrading automation around it
