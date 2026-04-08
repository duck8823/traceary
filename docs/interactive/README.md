# Interactive ergonomics

[日本語](./README.ja.md)

This note tracks the current state of Traceary's interactive inspection UX.
It records what `v0.1.9` improved directly and what remains intentionally deferred to a later release line.

## Landed in `v0.1.9`

### Shell completion

Traceary now exposes a built-in completion generator:

```sh
traceary completion bash
traceary completion zsh
traceary completion fish
traceary completion powershell
```

This is the smallest practical improvement that helps daily interactive use without changing the event model or read-path semantics.

## Still deferred

The following ideas remain valid, but they are intentionally not part of `v0.1.9`:

- `tail` / `watch` style live-follow views
- richer human-readable formatting for `show` / `context`
- pager-aware output flows
- more opinionated interactive filters layered on top of `list` / `search`

These are closer to a `v0.2` UX pass than to a small `v0.1.x` polish change.

## Current recommendation

Until a richer interactive mode lands:

1. use `traceary list --limit ... --offset ...` for a recent feed
2. use `traceary search ... --json` for filtered lookup
3. use `traceary show <event-id> --json` when you need structured detail
4. enable shell completion so command discovery does not depend only on memory

## Related docs

- CLI reference: [`../cli/README.md`](../cli/README.md)
- MCP guide: [`../mcp/README.md`](../mcp/README.md)

