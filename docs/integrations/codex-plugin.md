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
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて").

## Memory activation strategy

Codex was the first host with full Traceary host-native activation (shipped
in v0.12); v0.13.0 extends the same activation contract to Claude and
Gemini using a two-file import-stub strategy. Codex remains a single-file
target. Accepted memories stay in Traceary's SQLite store as the source of
truth, and the activation command writes only a Traceary-managed block into
the Codex memory target (`~/.codex/memories/traceary.md` by default):

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target codex --apply
traceary doctor --client codex --json
```

The apply path creates the target directory/file when needed, preserves
user-authored content outside the managed block, is idempotent when the
accepted memory set has not changed, and refuses newer managed-block marker
versions. See the [host-native memory activation ADR](../architecture/host-native-memory-activation.md)
for the full safety contract and the
[durable memory guide](../memory/README.md#activation-strategy-by-host) for
the cross-host strategy comparison and `invalid` recovery steps.

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

## Legacy install removal (v0.14.0)

`traceary integration codex install` was retired in **v0.14.0** (#920) and is no longer a working install path. New installs must use the official `/plugins` flow documented above. Invoking the legacy command exits with a usage error that names the v0.14.0 removal and points at the Codex `/plugins` flow.

### Cleanup-only uninstall (hidden, scheduled for v0.15 removal)

`traceary integration codex uninstall` is retained as a hidden cleanup-only command for users migrating off the retired install path. It is absent from `traceary integration codex --help` so it stays invisible to new users while remaining executable for cleanup scripts.

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

The cleanup-only uninstall removes the Traceary-managed plugin cache, the `[plugins."traceary@local-traceary-plugins"]` entry from `~/.codex/config.toml`, and Traceary-managed hook entries from `~/.codex/hooks.json`. Unrelated hooks and the `[features].codex_hooks` flag are left untouched.

The hidden uninstall command is scheduled for removal in **v0.15**. Run it once after migrating to the official `/plugins` install to avoid duplicate prompt / audit recordings, then drop the call from any automation.
