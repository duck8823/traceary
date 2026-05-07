# Interactive ergonomics

[日本語](./README.ja.md)

This note explains how to inspect Traceary interactively with the CLI that ships today.
It focuses on read-side workflows for humans rather than on the write-side hook automation.

## What changed recently

Traceary now ships two baseline interactive conveniences:

- shell completion
- `traceary tail` for live-follow inspection

That means the interactive read path is no longer limited to one-shot snapshots such as `list` and `search`.

## Recommended interactive workflow

Use the commands below according to the question you are trying to answer.

### 1. "What just happened?" → `traceary list`

Use `list` when you want a quick recent feed and already know the structured filters you care about.

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

### 2. "Which sessions are running right now?" → `traceary top`

Use `top` to watch a live multi-pane dashboard of the workspace. The screen is split into four panes:

- **sessions** — active session tree (workspace, agent role, latest event time, latest event as `<kind>: <message>`)
- **failures** — recent failed `command_executed` events
- **commands** — recent `command_executed` events
- **candidates** — durable-memory inbox candidates ordered by remember-intent priority

```sh
traceary top
traceary top --workspace github.com/duck8823/traceary
traceary top --snapshot
traceary top --snapshot --json
```

Inside the dashboard `tab` / `shift+tab` cycle the focused pane, `↑/↓` (or `k/j`) scroll it by one row, `pgup/pgdn` page through it, `g/G` jump to the top/bottom, `r` forces a refresh, `?` toggles help, and `q` / Ctrl-C / Esc quit cleanly. Non-TTY callers (pipes, CI logs) fall back to the snapshot text writer automatically. `--snapshot` and `--snapshot --json` mirror the four panes for scripts: the text snapshot prints `ACTIVE SESSIONS`, `RECENT FAILURES`, `RECENT COMMANDS`, and `CANDIDATE MEMORIES (count=N)` sections; the JSON snapshot is wrapped in an envelope with `sessions`, `failures`, `recent_commands`, and `candidates` (`{ count, items }`) keys.

### 3. "Is the system writing events right now?" → `traceary tail`

Use `tail` when you want to watch new events arrive in real time.
This is the best command for confirming that hooks are firing, that the expected workspace is receiving writes, or that failures are visible as they happen.

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 4. "Find a specific error / command / note" → `traceary search`

Use `search` for text lookup combined with time or workspace filters.

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 5. "Show me the full structured record" → `traceary show`

Use `show` when you already have an event ID and want the structured event or audit payload.

```sh
traceary show evt_123 --json
```

### 6. "Walk through candidate durable memories" → `traceary memory inbox review`

Use `memory inbox review` for an interactive walk through the durable-memory candidate inbox. It is TTY-only — non-interactive shells receive a refusal with exit code `2` and pointers to `traceary memory inbox list / accept / reject`. The same filters as the snapshot view are accepted (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`).

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

Inside the screen the action keys are `a` accept, `x` reject, `s` skip, `e` edit/distill, `v` view evidence, `?` help, `q` quit. Accept / reject reuse the same application use cases as `memory inbox accept|reject`. `e` opens an editor prompt that requires you to type a new operator-authored fact and routes through `traceary memory store distill` (no auto-accept of LLM output).

### 7. "What context should I carry into the next session?" → `traceary handoff`

Use `handoff` when you want a concise working-memory pack instead of the raw event stream.
This is the operator-facing summary view for resuming work or handing context to another agent.

```sh
traceary handoff --workspace github.com/duck8823/traceary
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

## Still future-facing

Interactive work is better than it was in early `v0.1.x`, but some improvements still belong to future UX passes:

- richer human-readable formatting for `show` / `context`
- pager-aware output flows
- more opinionated interactive filters layered on top of `list` / `search`

## Related docs

- CLI reference: [`../cli/README.md`](../cli/README.md)
- MCP guide: [`../mcp/README.md`](../mcp/README.md)
- Event lifecycle: [`../lifecycle.md`](../lifecycle.md)
