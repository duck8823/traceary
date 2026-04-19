# Codex plugin

[日本語](./codex-plugin.ja.md)

The Codex package lives under `plugins/traceary/`.
Traceary installs it through a first-class CLI entrypoint because Codex needs two different pieces for the full experience:

- the plugin itself for MCP, skills, and slash-style commands
- `~/.codex/hooks.json` plus `codex_hooks` for automatic session / prompt / audit recording

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart`, `UserPromptSubmit`, `Stop`, and `PostToolUse` hooks
- slash commands: `/traceary:help` and `/traceary:doctor`
- contextual skill: `traceary-session-history`

## Install from a local checkout

Codex does not currently expose a public plugin-install CLI equivalent to Claude or Gemini, so Traceary ships a dedicated `traceary integration codex ...` flow that installs both the plugin runtime and the Traceary hook wiring.

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

3. Install the packaged plugin, enable it in Codex config, and merge the Traceary hooks.

```sh
cd ~/src/traceary
traceary integration codex install
```

By default, that command:

- copies `plugins/traceary/` into a local marketplace root under `~/.agents/plugins`
- installs the active plugin cache under `~/.codex/plugins/cache/local-traceary-plugins/traceary/local`
- enables `[plugins."traceary@local-traceary-plugins"]` in `~/.codex/config.toml`
- enables `[features].codex_hooks = true`
- merges Traceary hook entries into `~/.codex/hooks.json`

If `traceary` is not available on `PATH`, pass `--traceary-bin /absolute/path/to/traceary`.
Use `--repo-root /path/to/traceary` when you run the command outside the repository checkout.

## Update

```sh
cd ~/src/traceary
git pull --ff-only
traceary integration codex install
```

## Uninstall

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

The uninstall command removes the Traceary plugin cache, the Traceary plugin config entry, and the Traceary-managed Codex hook entries. It intentionally leaves `[features].codex_hooks` enabled so other local hook workflows do not break.

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

That smoke test verifies the CLI-managed plugin cache, config, and hook files. Set `TRACEARY_ENABLE_CODEX_RUNTIME_SMOKE=1` when you also want an authenticated runtime probe on a machine that already has Codex CLI access configured.
