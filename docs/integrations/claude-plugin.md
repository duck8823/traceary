# Claude Code plugin

[日本語](./claude-plugin.ja.md)

The Claude package lives under `integrations/claude-plugin/` and is published through the repository-root Claude marketplace manifest at `.claude-plugin/marketplace.json`.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `PostToolUse` / `PostToolUseFailure` audit hooks for `Bash`, `mcp__.*`, and the built-in tool matcher (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`)
- slash-style skills: `/traceary-help` plus the contextual `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember` skills. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて") and writes durable memory directly. The legacy `traceary-memory-capture` skill is retained as a deprecated stub (will be removed in v0.12).

## Memory activation strategy

Claude integration in v0.12 uses Traceary's accepted memory store through MCP
tools and instruction-file export. To make reviewed memories visible in Claude
project instructions, export them into a Traceary-managed block:

```sh
traceary memory export --target claude --out CLAUDE.md
```

This is different from Codex host-native activation. `traceary memory activate
--target claude` is **not implemented** in v0.12, and the Claude plugin does not
write Claude-native memory files. If you own a direct Anthropic SDK loop, the
experimental native memory-tool backend remains available via
[`anthropic-memory-tool`](./anthropic-memory-tool.md), but that store is
separate from the curated `memories` aggregate. Follow-up #883 tracks a future
safe Claude-native activation path.

## Install

1. Install the Traceary CLI first.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Add this repository as a Claude marketplace.

```sh
/plugin marketplace add duck8823/traceary
```

3. Install the Traceary plugin from that marketplace.

```sh
/plugin install traceary
```

Run these commands inside Claude Code. Project/local scoping is controlled by Claude Code during the `/plugin install` flow.

## Update

```sh
/plugin marketplace update traceary-plugins
/plugin update traceary
```

> **Important**: `brew upgrade traceary` refreshes the CLI binary but **does not touch the Claude plugin cache**. When new Traceary releases add hooks (for example the v0.8 transcript and built-in-tool matcher hooks), you must also run `/plugin update traceary` before the newer hooks become active in a running Claude Code session. `traceary doctor --client claude` now surfaces a `claude-plugin-cache` check that warns when the cached version is behind the marketplace manifest.

## Uninstall

```sh
/plugin uninstall traceary
```

If you no longer need the marketplace at all:

```sh
/plugin marketplace remove traceary-plugins
```

## When the plugin is installed, `hooks install` is not needed

`traceary hooks install --client claude` writes Traceary hooks into a Claude settings file. The plugin installation already delivers the same hooks to Claude Code, so **running `hooks install` on top of the plugin would cause every audit event to be recorded twice**.

- `traceary hooks install --client claude` detects the active plugin (reads `~/.claude/settings.json`'s `enabledPlugins`) and skips with a message; use `--force` if you really want both registrations (for plugin development)
- `traceary doctor --client claude` reports this situation as a `warn` when the plugin is active and settings.json also contains Traceary-managed hooks

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client claude --json
traceary doctor --client claude --fix
traceary doctor --client claude --fix --dry-run
```

`--fix` is intentionally conservative: it can install or upgrade Traceary-managed hooks when the plugin is not active and can register the `traceary mcp-server` entry in Claude settings, backing up an existing settings file before changing the MCP block. It does not auto-update plugin versions or remove double registrations; those remain guided warnings with the upgrade/removal command in the doctor output.

Local package validation:

```sh
claude plugins validate .claude-plugin/marketplace.json
claude plugins validate integrations/claude-plugin
```

End-to-end smoke test from this repository:

```sh
./scripts/smoke_test_integrations.sh claude
```
