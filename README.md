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
- work is split across Claude, Codex, Antigravity (Gemini CLI for legacy installs), and manual terminal steps
- multiple sessions and worktree moves make the timeline harder to follow

Traceary keeps those records in one local SQLite store so the same history can be reused from the CLI, hooks, and MCP clients.

## Three-layer model

Traceary is no longer just a local event log. `v0.5.0` organizes the product around three layers that map to how agent workflows actually need context.

| Layer | What lives there | How it is fed |
|---|---|---|
| Audit / Archive | raw events (prompts, transcripts, command audits), session boundaries | host hooks (`SessionStart`, `UserPromptSubmit` / `BeforeAgent`, `PostToolUse` / `AfterTool`, `Stop` / `AfterAgent`, `PreCompact` / `PreCompress`, `SessionEnd`) — see [host coverage matrix](./docs/hooks/host-coverage.md) |
| Working memory | handoff / context packs assembled from recent sessions | derived on demand by `traceary session handoff` / MCP `get_context`. Claude `PreCompact` digest also syncs into `sessions.summary` so timeline / handoff have a useful header before the session ends |
| Durable memory | reusable facts such as decisions, constraints, preferences, and artifact refs | curated through the `traceary-memory-review` skill (review-intent triggers) and the `traceary-memory-remember` skill (explicit-write triggers) |

In practice, Traceary acts as a local-first memory substrate for AI agents: hooks feed L1 mechanically, L2 is recomputed when the next session starts, and L3 stays small because it only grows when the operator (or an explicit "remember that" verb) says so.

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

After installing, run `traceary` in an interactive terminal to open the Tail-first TUI. In scripts, pipes, or CI where no TTY is attached, call the script-friendly subcommand directly (`traceary list`, `traceary sessions --snapshot [--json]`, `traceary doctor --json`, etc.); `traceary top --snapshot [--json]` remains available as a permanent compatibility alias, and `traceary tui` remains an explicit compatibility entrypoint for the same cockpit.

### Step 2: Install the plugin for your agent host

**Claude Code** ([guide](./docs/integrations/claude-plugin.md))

```sh
/plugin marketplace add duck8823/traceary
/plugin install traceary
```

**Codex** ([guide](./docs/integrations/codex-plugin.md))

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
cd ~/src/traceary
codex   # then, inside Codex: /plugins -> Traceary Plugins -> Traceary
```

The `traceary integration codex install` helper was retired in v0.14.0 and the cleanup-only `traceary integration codex uninstall` surface was removed in v0.15.0. Use Codex CLI's official `/plugins` flow shown above for install / uninstall. See the [Codex plugin guide](./docs/integrations/codex-plugin.md) for migration details and manual cleanup steps for legacy installs.

**Google agent hosts:** install the **Antigravity** plugin unless your organization still uses Gemini CLI through Gemini Code Assist Standard/Enterprise or a paid API key. Google stopped serving Gemini CLI to free and Google AI Pro/Ultra users on 2026-06-18 and directs them to Antigravity. Traceary therefore maintains the Gemini extension for those enterprise and paid-API installations, but does not recommend it for new free/Pro/Ultra setups. See [Google's transition announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

For Antigravity, the quickest route is to install Traceary's hooks directly with `traceary hooks install --client antigravity`. The [Antigravity hooks and plugin guide](./docs/integrations/antigravity.md) distinguishes that route from installing the packaged plugin through `agy plugin install`. Existing supported Gemini CLI installations can continue with the [Gemini extension maintenance guide](./docs/integrations/gemini-extension.md), which includes the migration path.

For the integration overview, use the [native integrations guide](./docs/integrations/README.md). Direct Anthropic API users can also try the experimental [native memory-tool backend](./docs/integrations/anthropic-memory-tool.md).

### Step 3: Verify

```sh
traceary doctor
```

For CI or smoke checks that should fail only on broken states, use
`traceary doctor --json --warnings-ok`. Warning-only reports then exit `0`,
while failures still exit `1` and the JSON report keeps the warning counts.

## Quick start

`traceary store init` is optional. Commands create the database and run migrations on demand.
Use `store init` only when you want to create the DB path up front or confirm write permissions before a session starts. The v0.8.x top-level alias `traceary init` was removed in v0.14.0; running it now returns Cobra's unknown-command error (use `traceary store init`). See the [CLI stability and deprecation policy](./docs/cli-stability.md) for the full list of v0.14 removals and replacements.

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
traceary memory store remember \
  --type decision \
  --workspace github.com/duck8823/traceary \
  --fact "Use traceary session handoff --compact-only for compact resume context" \
  --evidence issue:#502

traceary session handoff --workspace github.com/duck8823/traceary
```

### 5. Curate the memory review queue

`traceary memory ...` is grouped by intent: `memory inbox` for candidate review, `memory store` for deliberate writes (`remember` / `propose` / `distill`), and `memory admin` for extraction, host-side I/O (`import` / `export` / `activate`), maintenance (`hygiene` / `graph`), and lifecycle (`supersede` / `expire` / `set-validity`). Daily-read commands (`memory search` / `memory show` / `memory list`) stay top-level. The flat verbs from earlier releases (`memory remember`, `memory accept`, ...) were removed in v0.15.0 after the v0.14 compatibility window; use the canonical grouped paths above.

For interactive review of the memory review queue at the terminal:

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

`memory inbox review` is a TTY-only walk-through built on a shared Bubble Tea TUI. Inside the screen `a` accept, `x` reject, `s` skip, `e` edit/distill, `r` attach evidence (and optional artifacts), `v` view evidence, `?` help, `q` quit. Edit/distill never auto-accepts LLM-authored text — it routes through `traceary memory store distill` and requires you to type the operator-authored fact. Non-interactive shells receive a refusal with exit code `2` and a pointer to the script-friendly fallback (`memory inbox list` plus `memory inbox accept|reject`, both of which now also accept a positional id and `--id-only`).

## Inspect recent and live activity

Traceary ships complementary inspection views so you can switch between "what's happening now" and "what happened across a span" without leaving the terminal:

| When | Command | Use it to |
|---|---|---|
| Starting from one operator cockpit | `traceary` (`traceary tui` explicitly) | follow live tail, session health, doctor warnings, recent failures, and jump into memory review |
| Watching the workspace dashboard | `traceary sessions` (`traceary top` permanent compatibility) | browse active sessions, recent failures / commands, memory candidates, and stale memories in one TUI |
| Following what is happening now | `traceary tail` | confirm hooks are firing, watch failures in real time |
| Understanding what happened across a span | `traceary timeline` | see gap-separated work blocks with a per-workspace activity summary |
| Inspecting raw events directly | `traceary list` / `traceary search` | jump to an exact kind / session / query |
| Resuming with assembled working memory | `traceary session handoff` | start a follow-up session with curated context |

### `traceary tui`

```sh
traceary tui
```

`traceary` opens the Tail-first operator cockpit in an interactive terminal. `traceary tui` remains the explicit compatibility entrypoint for the same cockpit: a TTY-only surface for live tail, session health, doctor warnings, recent failures, and new events, with a dedicated Memory tab for candidate review. The cockpit Sessions tab intentionally stays session-centric (sessions, failures, commands, and health); memory candidate and stale-memory review belong in the Memory tab. In non-interactive shells, bare `traceary` prints deterministic help/fallback guidance so scripts should continue calling explicit commands such as `traceary list`, `traceary sessions --snapshot [--json]`, `traceary top --snapshot [--json]`, and `traceary doctor --json`.

### `traceary sessions`

```sh
traceary sessions
```

`sessions` opens a five-pane Bubble Tea dashboard for active sessions, recent failures, recent commands, memory candidates, and stale memories. Those memory panes remain in the standalone Sessions dashboard and in `traceary sessions --snapshot [--json]` / `traceary top --snapshot [--json]` for compatibility while the cockpit Sessions tab stays narrower. Use `tab` / `shift+tab` to move between panes, `/` to filter the focused pane incrementally, and Enter to drill into the highlighted session, event, or memory detail. In non-TTY shells, `traceary sessions --snapshot` and `traceary sessions --snapshot --json` expose the same data for scripts, including the `stale_memories` envelope key. `traceary top` remains available as a permanent compatibility alias for existing scripts.

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
| Gemini CLI | Full (`SessionStart` + `SessionEnd`) | Tool hooks | No | No | Basic (legacy) |
| Antigravity | Full (`PreInvocation` start; `Stop` turn boundary) | `run_command` hooks (`PreToolUse` + `PostToolUse` paired) | No | No | Partial |

> **v0.21.1 note:** Gemini CLI is the legacy Google AI agent host. Antigravity (`/Applications/Antigravity.app`, bundle ID `com.google.antigravity`) is the active successor. As of v0.21.1, Traceary supports Antigravity as a real hook client with a packaged plugin (`integrations/antigravity-plugin/`) against the documented public hook surface. Install with `traceary hooks install --client antigravity`. The Gemini CLI extension package remains available for existing installs. See the [Antigravity hooks and plugin guide](./docs/integrations/antigravity.md).
>
> 2026 Q2 note: Claude Code's `SubagentStop` / `PreCompact` hooks are available but not wired into Traceary's managed hook set. The Codex memory feature flag in `~/.codex/config.toml` changes Codex's own capture behaviour, not Traceary's — `traceary memory admin import codex` works regardless. `traceary doctor` surfaces the same notes under `<client>-host-capabilities`, and the full list lives in the [hook contract](./docs/hooks/contract.md#2026-q2-host-capability-notes).

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
- [CLI stability and deprecation policy](./docs/cli-stability.md)
- [Hooks guide](./docs/hooks/README.md)
- [Hook contract and capability tiers](./docs/hooks/contract.md)
- [Event lifecycle](./docs/lifecycle.md)
- [MCP guide](./docs/mcp/README.md)
- [Environment and storage notes](./docs/environment/README.md)

## Contributing and support

- bug reports and feature requests belong in GitHub Issues
- security reports should follow [SECURITY.md](./SECURITY.md)
- this is an actively evolving `v0.x` OSS tool, so check the [changelog](./CHANGELOG.md) before upgrading automation around it
