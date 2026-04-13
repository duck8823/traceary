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

### 2. "Is the system writing events right now?" → `traceary tail`

Use `tail` when you want to watch new events arrive in real time.
This is the best command for confirming that hooks are firing, that the expected workspace is receiving writes, or that failures are visible as they happen.

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 3. "Find a specific error / command / note" → `traceary search`

Use `search` for text lookup combined with time or workspace filters.

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 4. "Show me the full structured record" → `traceary show`

Use `show` when you already have an event ID and want the structured event or audit payload.

```sh
traceary show evt_123 --json
```

### 5. "What context should I carry into the next session?" → `traceary handoff`

Use `handoff` when you want a concise working-memory pack instead of the raw event stream.
This is the operator-facing summary view for resuming work or handing context to another agent.

```sh
traceary handoff --workspace github.com/duck8823/traceary
traceary session handoff --session-id sess_123 --json
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
