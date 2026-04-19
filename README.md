# Traceary

[日本語](./README.ja.md)

<p align="center">
  <img src="./docs/assets/traceary-mark.svg" alt="Traceary mark" width="120">
</p>

[![CI](https://github.com/duck8823/traceary/actions/workflows/ci.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/ci.yml)
[![Release](https://github.com/duck8823/traceary/actions/workflows/release.yml/badge.svg?event=push)](https://github.com/duck8823/traceary/actions/workflows/release.yml)

Traceary is a local-first CLI and MCP server for recording and searching AI agent work logs, session boundaries, and shell command audits.

Install the CLI first, then add the plugin for your AI agent host to enable automatic recording.

## Why Traceary

AI-assisted development gets messy quickly when:

- session context disappears after `clear` or `compact`
- Git history explains what changed, but not always why
- shell command output is hard to connect back to the right agent or session
- work is split across Claude, Codex, Gemini, and manual terminal steps
- multiple sessions and worktree moves make the timeline harder to follow

Traceary keeps those records in one local SQLite store so the same history can be reused from the CLI, hooks, and MCP clients.

## Three-layer model

Traceary is no longer just a local event log. `v0.5.0` organizes the product around three layers that map to how agent workflows actually need context.

| Layer | What lives there | Why it matters |
|---|---|---|
| Audit / Archive | raw events, session boundaries, command audits | keeps the source-of-truth timeline for inspection, search, and forensic review |
| Working memory | handoff/context packs assembled from recent sessions | helps the next agent or resumed session start with the right context instead of the whole log |
| Durable memory | reusable facts such as decisions, constraints, preferences, and artifact refs | stores the small set of facts that should survive across sessions and be retrieved on demand |

In practice, this means Traceary can act as a local-first memory substrate for AI agents rather than only a CLI that appends logs.

Traceary is local-first. It writes to SQLite on your machine and does not include built-in telemetry, analytics, or hosted storage.

## Getting started

### Step 1: Install the Traceary CLI

The CLI is required first — agent plugins invoke the `traceary` binary via hooks.

```sh
# Homebrew (recommended)
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary

# or go install
go install github.com/duck8823/traceary@latest
```

Tagged releases also publish macOS and Linux archives on [GitHub Releases](https://github.com/duck8823/traceary/releases).
See the [release guide](./docs/release/README.md) for packaging details.

### Step 2: Install the plugin for your agent host

**Claude Code** ([guide](./docs/integrations/claude-plugin.md))

```sh
claude plugins marketplace add https://github.com/duck8823/traceary
claude plugins install traceary@traceary-plugins --scope user
```

**Codex** ([guide](./docs/integrations/codex-plugin.md))

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
cd ~/src/traceary
codex   # then, inside Codex: /plugins -> Traceary Plugins -> Traceary
```

The legacy `traceary integration codex install` helper still works as a transitional path for earlier Traceary releases, but is deprecated and will be removed no earlier than v0.8.0. See the [Codex plugin guide](./docs/integrations/codex-plugin.md) for migration details.

**Gemini CLI** ([guide](./docs/integrations/gemini-extension.md))

```sh
bash <(curl -sL https://raw.githubusercontent.com/duck8823/traceary/main/scripts/install-gemini-extension.sh)
```

For the integration overview, use the [native integrations guide](./docs/integrations/README.md).

### Step 3: Verify

```sh
traceary doctor
```

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

### 4. Promote reusable context when it matters

```sh
traceary memory remember \
  --type decision \
  --workspace github.com/duck8823/traceary \
  --fact "Use traceary handoff for compact resume context" \
  --evidence issue:#502

traceary handoff --workspace github.com/duck8823/traceary
```

## Inspect recent and live activity

Traceary ships four complementary inspection views so you can switch between "what's happening now" and "what happened across a span" without leaving the terminal:

| When | Command | Use it to |
|---|---|---|
| Following what is happening now | `traceary tail` | confirm hooks are firing, watch failures in real time |
| Understanding what happened across a span | `traceary timeline` | see gap-separated work blocks with a per-workspace activity summary |
| Inspecting raw events directly | `traceary list` / `traceary search` | jump to an exact kind / session / query |
| Resuming with assembled working memory | `traceary handoff` | start a follow-up session with curated context |

### `traceary tail`

```text
$ traceary tail --limit 3
07:06:44  command_executed  sess=4a70c526  ws=traceary  ls ~/.traceary 2>&1; find ~ -name "traceary…
07:06:47  command_executed  sess=4a70c526  ws=traceary  ./traceary timeline --db-path /Users/duck88…
07:06:52  command_executed  sess=4a70c526  ws=traceary  timeout 1 ./traceary tail --db-path /Users/…
```

Compact single-line rows (local time by default) fit inside ~100 columns. Add `--wide --utc` to restore the pre-v0.6.1 tab-separated seven-column layout byte-for-byte, or `--json` for NDJSON when piping into tooling.

### `traceary timeline`

```text
$ traceary timeline --limit 2
2026-04-15 06:37 - 07:06 (29m21s) total events: 165
  github.com/duck8823/traceary (153) — 自律的に進めてください。
  github.com/duck8823/dotfiles  ( 12) — rust インストールしました
2026-04-15 05:39 - 06:10 (31m1s) total events: 136
  github.com/duck8823/traceary (136) — <analysis> This conversation is a resumption after compaction. …
```

Each block shows one sub-row per workspace with an activity summary picked from the fallback chain **`compact_summary` → first `prompt` → kind counts**, so you can see *what was being done* in each workspace instead of just a comma-joined list. Pass `--utc` to switch text timestamps to UTC; `--json` adds a `workspace_breakdown` array alongside the existing block fields.

See [`docs/cli/README.md`](./docs/cli/README.md) for the full flag reference and more examples.

## Host capture matrix

The query surface is shared: once Traceary is installed, every host can use the same CLI and MCP memory/context commands. What differs is how much context each host can capture automatically via hooks.

| Host | Session lifecycle | Tool audit | Prompt capture | Compact-summary capture | Automatic capture tier |
|---|---|---|---|---|---|
| Claude Code | Full | Bash + MCP + failure hooks | Yes | Yes | Full |
| Codex | Full (`SessionStart` + `Stop`) | Tool hooks | Yes | No | Partial |
| Gemini CLI | Full (`SessionStart` + `SessionEnd`) | Tool hooks | No | No | Basic |

> 2026 Q2 note: Claude Code's `SubagentStop` / `PreCompact` hooks and Gemini CLI 0.38.x's memory-manager preview are available but not wired into Traceary's managed hook set. The Codex memory feature flag in `~/.codex/config.toml` changes Codex's own capture behaviour, not Traceary's — `traceary memory import codex` works regardless. `traceary doctor` surfaces the same notes under `<client>-host-capabilities`, and the full list lives in the [hook contract](./docs/hooks/contract.md#2026-q2-host-capability-notes).

For the full contract and hook semantics, see the [hook contract](./docs/hooks/contract.md) and [event lifecycle](./docs/lifecycle.md).

## Defaults worth knowing

- `traceary log` and `traceary audit` reuse the latest non-stale active session for the resolved workspace when `--session-id` is omitted; when `remote.origin.url` is missing inside a git worktree, Traceary falls back to the worktree root path
- `traceary session active` treats sessions older than `24h` as stale unless you pass `--allow-stale`
- `traceary session start` prints a session ID; `traceary session end` prints the recorded event ID
- `traceary session list --json` includes `label`, `summary`, and `parent_session_id` when present
- default operator-facing CLI output is English; set `TRACEARY_LANG=ja` when you want Japanese messaging
- `--json` output stays language-neutral

## Documentation

Use the [documentation index](./docs/README.md) for the full map.
The most common next pages are:

- [Native integrations](./docs/integrations/README.md)
- [Architecture principles](./docs/architecture/README.md)
- [Durable memory guide](./docs/memory/README.md)
- [CLI reference](./docs/cli/README.md)
- [Hooks guide](./docs/hooks/README.md)
- [Hook contract and capability tiers](./docs/hooks/contract.md)
- [Event lifecycle](./docs/lifecycle.md)
- [MCP guide](./docs/mcp/README.md)
- [Environment and storage notes](./docs/environment/README.md)

## Contributing and support

- bug reports and feature requests belong in GitHub Issues
- security reports should follow [SECURITY.md](./SECURITY.md)
- this is an actively evolving `v0.x` OSS tool, so check the [changelog](./CHANGELOG.md) before upgrading automation around it
