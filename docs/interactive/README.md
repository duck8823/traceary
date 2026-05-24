# Interactive ergonomics

[日本語](./README.ja.md)

This note explains how to inspect Traceary interactively with the CLI that ships today.
It focuses on read-side workflows for humans rather than on the write-side hook automation.

## What changed recently

Traceary now ships three baseline interactive conveniences:

- bare `traceary` as the Tail-first operator cockpit entrypoint, with `traceary tui` kept as the explicit compatibility path
- shell completion
- `traceary tail` for live-follow inspection

That means the interactive read path is no longer limited to one-shot snapshots such as `list` and `search`.
The cockpit is the recommended starting point when you do not want to remember whether the next action is `top`, `tail`, `doctor`, `session handoff`, or `memory inbox review`.

## Recommended interactive workflow

Use the commands below according to the question you are trying to answer.

### 1. "I want one place to start" → `traceary`

Use bare `traceary` when you are at an interactive terminal and want Traceary to show the Tail-first operator cockpit first. `traceary tui` remains the explicit compatibility entrypoint for the same cockpit. The cockpit summarizes active work, doctor warnings/failures, recent failures, new events since the last live-tail visit, and memory candidates queued since the last memory review. From there you can jump into:

- live event tail
- doctor details
- memory inbox review

```sh
traceary
traceary tui
traceary tui --reset-state
```

The cockpit is intentionally TTY-only. Non-interactive callers should keep using `traceary list`, `traceary top --snapshot [--json]`, `traceary doctor --json`, `traceary session handoff`, and `traceary memory inbox list`. Bare non-TTY `traceary` prints help plus fallback guidance instead of launching the cockpit.

### 2. "What just happened?" → `traceary list`

Use `list` when you want a quick recent feed and already know the structured filters you care about.

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

### 3. "Which sessions are running right now?" → `traceary top`

Use `top` to watch a live multi-pane dashboard of the workspace. The screen is split into five panes:

- **sessions** — active session tree (workspace, agent role, latest event time, latest event as `<kind>: <message>`)
- **failures** — recent failed `command_executed` events
- **commands** — recent `command_executed` events
- **candidates** — memory review queue candidates ordered by remember-intent priority
- **stale memories** — accepted memories that may need cleanup

```sh
traceary top
traceary top --workspace github.com/duck8823/traceary
traceary top --snapshot
traceary top --snapshot --json
```

Inside the dashboard `tab` / `shift+tab` cycle the focused pane, `↑/↓` (or `k/j`) scroll it by one row, `pgup/pgdn` page through it, `g/G` jump to the top/bottom, `r` forces a refresh, `?` toggles help, and `q` / Ctrl-C / Esc quit cleanly. Non-TTY callers (pipes, CI logs) fall back to the snapshot text writer automatically. `--snapshot` and `--snapshot --json` mirror the five panes for scripts: the text snapshot prints `ACTIVE SESSIONS`, `RECENT FAILURES`, `RECENT COMMANDS`, `CANDIDATE MEMORIES (count=N)`, and `STALE MEMORIES (count=N)` sections; the JSON snapshot is wrapped in an envelope with `sessions`, `failures`, `recent_commands`, `candidates` (`{ count, items }`), and `stale_memories` (`{ count, items }`) keys.

### 4. "Is the system writing events right now?" → `traceary tail`

Use `tail` when you want to watch new events arrive in real time.
This is the best command for confirming that hooks are firing, that the expected workspace is receiving writes, or that failures are visible as they happen.

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 5. "Find a specific error / command / note" → `traceary search`

Use `search` for text lookup combined with time or workspace filters.

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 6. "Show me the full structured record" → `traceary show`

Use `show` when you already have an event ID and want the structured event or audit payload.

```sh
traceary show evt_123 --json
```

### 7. "Walk through memory candidates" → `traceary memory inbox review`

Use `memory inbox review` for an interactive walk through the memory review queue. It is TTY-only — non-interactive shells receive a refusal with exit code `2` and pointers to `traceary memory inbox list / accept / reject`. The same filters as the snapshot view are accepted (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`).

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

Inside the screen the action keys are `a` accept, `x` reject, `s` skip, `e` edit/distill, `v` view evidence, `?` help, `q` quit. Accept / reject reuse the same application use cases as `memory inbox accept|reject`. `e` opens an editor prompt that requires you to type a new operator-authored fact and routes through `traceary memory store distill` (no auto-accept of LLM output).

### 8. "What context should I carry into the next session?" → `traceary session handoff`

Use `session handoff` when you want a concise working-memory pack instead of the raw event stream.
This is the operator-facing summary view for resuming work or handing context to another agent. (The v0.13.x top-level `traceary handoff` alias was removed in v0.14.0.)

```sh
traceary session handoff --workspace github.com/duck8823/traceary
traceary session handoff --session-id sess_123
```

## Shell completion

Traceary exposes a built-in completion generator:

```sh
traceary completion bash
traceary completion zsh
traceary completion fish
traceary completion powershell
```

Completion is still worth enabling even after `tail` landed, because it reduces command discovery friction for the broader CLI surface.

## Bare `traceary` entrypoint policy

For v0.19.0, bare `traceary` opens the Tail-first cockpit when stdin/stdout are attached to an interactive terminal. Running `traceary` with no subcommand in a non-TTY context keeps deterministic help/fallback output instead of starting Bubble Tea.

The compatibility contract is:

- `traceary tui` remains a stable explicit entrypoint for operators who prefer a named command.
- Non-TTY `traceary` must keep deterministic help/script behavior.
- Completion generation and help examples must remain stable.
- Script-facing commands (`top --snapshot`, `tail`, `doctor --json`, `session handoff`, `memory inbox list`) remain the recommended automation path.
- Release notes must call out the default-entrypoint change and the explicit `traceary tui` compatibility path.

## Still future-facing

Interactive work is better than it was in early `v0.1.x`, but some improvements still belong to future UX passes:

- richer human-readable formatting for `show` / `context`
- pager-aware output flows
- more opinionated interactive filters layered on top of `list` / `search`

## Related docs

- CLI reference: [`../cli/README.md`](../cli/README.md)
- MCP guide: [`../mcp/README.md`](../mcp/README.md)
- Event lifecycle: [`../lifecycle.md`](../lifecycle.md)
