# Claude Code plugin

[日本語](./claude-plugin.ja.md)

The Claude package lives under `integrations/claude-plugin/` and is published through the repository-root Claude marketplace manifest at `.claude-plugin/marketplace.json`.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `PostToolUse` / `PostToolUseFailure` shell-audit hooks for Bash
- slash-style skills: `/traceary-help` plus the contextual `traceary-session-history` and `traceary-memory-capture` skills (the latter prompts the agent to proactively call `propose_memory` when the conversation surfaces a durable decision / constraint / lesson / preference / artifact)

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
claude plugins marketplace add https://github.com/duck8823/traceary
```

3. Install the Traceary plugin from that marketplace.

```sh
claude plugins install traceary@traceary-plugins --scope user
```

Use `--scope project` or `--scope local` when you do not want a user-wide install.

## Update

```sh
claude plugins marketplace update traceary-plugins
claude plugins update traceary@traceary-plugins
```

## Uninstall

```sh
claude plugins uninstall traceary@traceary-plugins
```

If you no longer need the marketplace at all:

```sh
claude plugins marketplace remove traceary-plugins
```

## When the plugin is installed, `hooks install` is not needed

`traceary hooks install --client claude` writes Traceary hooks into a Claude settings file. The plugin installation already delivers the same hooks to Claude Code, so **running `hooks install` on top of the plugin would cause every audit event to be recorded twice**.

- `traceary hooks install --client claude` detects the active plugin (reads `~/.claude/settings.json`'s `enabledPlugins`) and skips with a message; use `--force` if you really want both registrations (for plugin development)
- `traceary doctor --client claude` reports this situation as a `warn` when the plugin is active and settings.json also contains Traceary-managed hooks

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client claude --json
```

Local package validation:

```sh
claude plugins validate .claude-plugin/marketplace.json
claude plugins validate integrations/claude-plugin
```

End-to-end smoke test from this repository:

```sh
./scripts/smoke_test_integrations.sh claude
```
