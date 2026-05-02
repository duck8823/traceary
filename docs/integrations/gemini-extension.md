# Gemini CLI extension

[日本語](./gemini-extension.ja.md)

The Gemini package lives under `integrations/gemini-extension/`. Gemini CLI expects `gemini-extension.json` at the root of the installed extension, so Traceary ships this package as a dedicated extension archive on tagged releases.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `AfterAgent` transcript hook — records the agent response as a `transcript` event
- `AfterTool` shell-audit hook for `run_shell_command`
- slash commands: `/traceary-help` and `/traceary-doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて"). The legacy `traceary-memory-capture` skill is retained as a deprecated stub (will be removed in v0.12).

## Memory activation strategy

Gemini integration in v0.12 uses Traceary's accepted memory store through MCP
tools and instruction-file export. To make reviewed memories visible in Gemini
instructions, export them into a Traceary-managed block:

```sh
traceary memory export --target gemini --out GEMINI.md
```

This is intentionally separate from Codex host-native activation. `traceary
memory activate --target gemini` is **not implemented** in v0.12, and the
extension does not write Gemini-native memory files. Follow-up #884 tracks a
future safe Gemini-native activation path once the host-native surface and
preview/feature-flag behavior are stable enough to target.

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
