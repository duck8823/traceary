# Gemini CLI extension

[日本語](./gemini-extension.ja.md)

The Gemini package lives under `integrations/gemini-extension/`. Gemini CLI expects `gemini-extension.json` at the root of the installed extension, so Traceary ships this package as a dedicated extension archive on tagged releases.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `AfterTool` shell-audit hook for `run_shell_command`
- slash commands: `/traceary-help` and `/traceary-doctor`
- contextual skills: `traceary-session-history` and `traceary-memory-capture` (the latter prompts the agent to proactively call `propose_memory` when the conversation surfaces a durable decision / constraint / lesson / preference / artifact)

## Install

1. Install the Traceary CLI first.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Install the extension from a Traceary GitHub release.

```sh
gemini extensions install https://github.com/duck8823/traceary --ref <tag>
```

Traceary publishes a dedicated `traceary.tar.gz` release asset whose archive root is the extension root expected by Gemini CLI.

For local development against this repository, use a link instead:

```sh
gemini extensions link integrations/gemini-extension
```

## Update

```sh
gemini extensions update traceary
```

To pin a specific release again, reinstall with `--ref <tag>`.

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client gemini --json
```

Package validation:

```sh
gemini extensions validate integrations/gemini-extension
```

End-to-end smoke test from this repository:

```sh
./scripts/smoke_test_integrations.sh gemini
```
