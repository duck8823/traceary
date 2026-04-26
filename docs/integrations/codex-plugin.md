# Codex plugin

[日本語](./codex-plugin.ja.md)

Traceary ships a Codex plugin under `plugins/traceary/` that plugs into Codex CLI's official `/plugins` flow.
Codex picks up the MCP server, the slash commands, the session-history skill, and the session / prompt / transcript / audit hooks automatically as soon as the plugin is installed through the official flow.

## Install via Codex's official /plugins flow (primary)

1. Install the Traceary CLI first — agent hooks invoke the `traceary` binary.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Clone this repository somewhere stable. The repository ships a local Codex marketplace at `.agents/plugins/marketplace.json` plus the plugin itself under `plugins/traceary/`.

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
```

3. Start Codex inside the repository so it discovers the bundled marketplace.

```sh
cd ~/src/traceary
codex
```

4. Inside Codex, open `/plugins`, choose `Traceary Plugins` as the marketplace, and install the `Traceary` plugin. Codex materializes the plugin into its managed cache and registers the hooks declared in the plugin manifest.

5. Open a new thread and verify:

```sh
traceary doctor --client codex --json
```

## What the official flow wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart`, `UserPromptSubmit`, `Stop` (session end + transcript), and `PostToolUse` hooks (declared in `plugins/traceary/hooks.json` and referenced from the plugin manifest)
- slash commands: `/traceary:help` and `/traceary:doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて"). The legacy `traceary-memory-capture` skill is retained as a deprecated stub (will be removed in v0.12).

## Update

Refresh the repository and let Codex pick up the new plugin version on the next `/plugins` refresh:

```sh
cd ~/src/traceary
git pull --ff-only
```

If you need to bounce the plugin, `/plugins` also exposes a reinstall entry for the currently installed version.

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client codex --json
```

Structural package validation (for maintainers who change the plugin manifest, hooks, or marketplace entry):

```sh
python3 scripts/verify_integrations.py
```

## Legacy install (deprecated, compatibility only)

`traceary integration codex install` is kept as a transitional path for users on earlier Traceary releases. It prints a deprecation banner to stderr and will be removed **no earlier than v0.8.0**.

The legacy command still performs every step manually — it copies the plugin into `~/.agents/plugins`, materializes the active cache under `~/.codex/plugins/cache/local-traceary-plugins/traceary/local`, enables the plugin in `~/.codex/config.toml`, and merges Traceary hooks into `~/.codex/hooks.json`. Scripts that parse its stdout keep working; only the stderr banner is new.

### Migrating from the legacy install

1. Run the official `/plugins` install above.
2. Once Codex confirms the plugin is enabled, clean up the legacy state once:

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

The uninstall path is intentionally kept for this cleanup. It removes the Traceary-managed plugin cache, the `[plugins."traceary@local-traceary-plugins"]` entry from `~/.codex/config.toml`, and the Traceary hook entries from `~/.codex/hooks.json`, leaving unrelated hooks and the `[features].codex_hooks` flag untouched. This avoids double-recording prompts and audits while both install paths are active.

Both `traceary integration codex install` and `traceary integration codex uninstall` are scheduled for removal after the v0.7.x window closes; plan your migration before that release line ends.
