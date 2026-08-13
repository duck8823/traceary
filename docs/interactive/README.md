# Interactive ergonomics

[日本語](./README.ja.md)

This note explains how to inspect Traceary interactively with the CLI that ships today.
It focuses on read-side workflows for humans rather than on the write-side hook automation.

## What changed recently

Traceary now ships these baseline interactive conveniences:

- bare `traceary` prints help (TTY and non-TTY); the former Tail-first cockpit entrypoint was removed in v0.35.0
- shell completion
- `traceary tail` for live-follow inspection
- `traceary sessions` / `traceary sessions --snapshot` for the Sessions snapshot view (the live interactive dashboard was removed in v0.35.0)
- `traceary memory inbox review` for TTY-only inbox walk-through

That means the interactive read path is no longer limited to one-shot snapshots such as `list` and `search`.

## Recommended interactive workflow

Use the commands below according to the question you are trying to answer.

### 1. "I want to see what commands exist" → `traceary`

Bare `traceary` (and `traceary --help`) always prints help. Prefer explicit read commands for real work:

```sh
traceary
traceary --help
traceary list
traceary sessions --snapshot
traceary doctor --json
```

The former operator cockpit (`traceary tui` / `traceary dashboard` and the bare TTY default that opened it) was removed in v0.35.0. An orphan local state file at `~/.local/state/traceary/cockpit.json` (or `$XDG_STATE_HOME/traceary/cockpit.json`) is safe to delete manually.

### 2. "What just happened?" → `traceary list`

Use `list` when you want a quick recent feed and already know the structured filters you care about.

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

<a id="3-which-sessions-are-running-right-now--traceary-top"></a>

### 3. "Which sessions are running right now?" → `traceary sessions`

Use `sessions` (byte-identical to `sessions --snapshot`) for the Sessions snapshot view. The live multi-pane dashboard was removed in v0.35.0 after the v0.34 deprecation window. The text snapshot covers five sections:

- **sessions** — active session tree (workspace, agent role, latest event time, latest event as `<kind>: <message>`)
- **failures** — recent failed `command_executed` events
- **commands** — recent `command_executed` events
- **candidates** — memory review queue candidates ordered by remember-intent priority
- **stale memories** — accepted memories that may need cleanup

```sh
traceary sessions
traceary sessions --workspace github.com/duck8823/traceary
traceary sessions --snapshot --json
```

Bare `sessions` and `sessions --snapshot` print the same text: the snapshot starts with `RELIABILITY`, then prints `ACTIVE SESSIONS`, `RECENT FAILURES`, `RECENT COMMANDS`, `CANDIDATE MEMORIES (count=N remember_intent=M)`, and `STALE MEMORIES (count=N)` sections. The JSON snapshot (`--snapshot --json`) is wrapped in an envelope with `sessions`, `failures`, `recent_commands`, `candidates` (`{ count, remember_intent_count, items }`), `stale_memories` (`{ count, items }`), and `reliability` keys. The former `traceary top` compatibility alias was removed in v0.35.0; use `traceary sessions` instead.

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

Use `memory inbox review` for an interactive walk through the memory review queue. It is TTY-only — non-interactive shells receive a refusal with exit code `2` and pointers to `traceary memory inbox list / accept / reject / attach`. The same filters as the snapshot view are accepted (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`).

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

Inside the screen the action keys are `a` accept, `x` reject, `s` skip, `r` attach evidence, `e` edit/distill, `v` view evidence, `?` help, `q` quit. Accept / reject / evidence attach reuse the same application use cases as `memory inbox accept|reject|attach`. `r` accepts comma-separated `kind:value` evidence refs and optional `artifact:kind:value` refs so evidence-less candidates can be substantiated before acceptance. `e` opens an editor prompt that requires you to type a new operator-authored fact and routes through `traceary memory store distill` (no auto-accept of LLM output).

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

Bare `traceary` always prints help (TTY and non-TTY). The former Tail-first cockpit default and `traceary tui` / `traceary dashboard` were removed in v0.35.0 (#1764).

The compatibility contract is:

- Bare `traceary` and `traceary --help` print help only.
- Completion generation and help examples must remain stable.
- Script-facing commands (`sessions --snapshot`, `tail`, `doctor --json`, `session handoff`, `memory inbox list`) remain the recommended automation path.

## Still future-facing

Interactive work is better than it was in early `v0.1.x`, but some improvements still belong to future UX passes:

- richer human-readable formatting for `show` / `context`
- pager-aware output flows
- more opinionated interactive filters layered on top of `list` / `search`

## Related docs

- CLI reference: [`../cli/README.md`](../cli/README.md)
- Event lifecycle: [`../lifecycle.md`](../lifecycle.md)
