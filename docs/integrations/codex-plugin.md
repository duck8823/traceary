# Codex plugin

[日本語](./codex-plugin.ja.md)

The Codex package lives under `plugins/traceary/` and is published in the repository-root Codex marketplace format at `.agents/plugins/marketplace.json`.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart`, `Stop`, and `PostToolUse` hooks
- slash commands: `/traceary:help` and `/traceary:doctor`
- contextual skill: `traceary-session-history`

## Install from a local checkout

Codex does not currently expose a public plugin-install CLI equivalent to Claude or Gemini, so Traceary ships helper scripts for the standard local plugin directory.

1. Install the Traceary CLI first.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Clone this repository somewhere stable.

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
```

3. Install the packaged plugin into `~/.agents/plugins`.

```sh
cd ~/src/traceary
python3 scripts/codex/install_plugin.py
```

That command copies `plugins/traceary/` into the standard local plugin directory and upserts the matching marketplace entry.

## Update

```sh
cd ~/src/traceary
git pull --ff-only
python3 scripts/codex/install_plugin.py
```

## Uninstall

```sh
cd ~/src/traceary
python3 scripts/codex/uninstall_plugin.py
```

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client codex --json
```

Structural package validation:

```sh
python3 scripts/verify_integrations.py
```

Local smoke test from this repository:

```sh
./scripts/smoke_test_integrations.sh codex
```

The Codex smoke test validates the packaged marketplace/plugin layout by default and can optionally attempt a runtime probe when a plugin-enabled Codex build is available.
