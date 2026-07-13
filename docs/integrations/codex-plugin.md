# Codex plugin

[日本語](./codex-plugin.ja.md)

Traceary ships a Codex plugin under `plugins/traceary/` that plugs into Codex CLI's official `/plugins` flow.
Codex picks up the MCP server, the slash commands, and the session-history skill automatically as soon as the plugin is installed through the official flow. Hook registration is conditional: it works on Codex builds where the `plugin_hooks` feature is enabled and stable. On builds where `plugin_hooks` is still under development or explicitly disabled (`codex features list` shows `plugin_hooks` as `under development`, or `~/.codex/config.toml` has `[features].plugin_hooks = false`), the plugin's hook manifest is not materialized into `~/.codex/hooks.json` and a manual hook install is required — see the **Hook fallback when plugin_hooks is unavailable** section below.

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
- `SessionStart`, `UserPromptSubmit`, `Stop` (turn-boundary transcript; not a session end — #1170), and `PostToolUse` hooks (declared in `plugins/traceary/hooks.json` and referenced from the plugin manifest) — **only when `plugin_hooks` is enabled on your Codex build**; otherwise see the fallback below
- slash commands: `/traceary:help` and `/traceary:doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて").

## Hook fallback when plugin_hooks is unavailable

Codex's plugin-managed hook support shipped behind the `plugin_hooks` feature flag and is still marked `under development` on some builds. When the feature is unavailable, Codex will not materialize the plugin manifest's hook declarations into `~/.codex/hooks.json`, so `traceary tail` and durable-memory extraction will see no Codex events even though `/plugins` lists the Traceary plugin as enabled.

Diagnose with:

```sh
codex features list                          # is plugin_hooks stable + on?
cat ~/.codex/config.toml                     # does [features].plugin_hooks = false exist?
traceary doctor --client codex --json        # surfaces the actionable fallback warning
```

If `plugin_hooks` is unavailable, install the hooks manually so events are still captured:

```sh
traceary hooks install --client codex --upgrade --traceary-bin "$(command -v traceary)"
traceary doctor --client codex --json
```

The fallback writes Traceary-managed entries directly into `~/.codex/hooks.json` (named `traceary-session-start`, `traceary-prompt`, `traceary-transcript`, `traceary-session-stop`, `traceary-audit`). Existing non-Traceary entries are preserved.

### Duplicate-capture warning

If a future Codex build enables `plugin_hooks` for your install (the plugin manifest's hooks start firing automatically) while these manual entries remain in `~/.codex/hooks.json`, **every session/prompt/transcript/audit event will be recorded twice**. After setting `[features].plugin_hooks = true`, let doctor detect and remove the obsolete manual path:

```sh
traceary doctor --client codex --json
traceary doctor --fix --dry-run --client codex
traceary doctor --fix --client codex
```

Doctor only offers this cleanup when the Traceary plugin is enabled and `plugin_hooks = true` explicitly confirms plugin-managed hooks. It removes the named Traceary-managed entries (`traceary-session-start`, `traceary-prompt`, `traceary-transcript`, `traceary-session-stop`, and `traceary-audit`) while preserving unrelated hooks and top-level fields. When the feature is false or unspecified, doctor keeps the manual fallback intact.

After cleanup, re-run `traceary doctor --client codex --json` to confirm only one registration path is active.

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
go run ./cmd/repo-tooling integrations verify
```

## Legacy install/uninstall removal (v0.14.0, v0.15.0)

`traceary integration codex install` was retired in **v0.14.0** (#920) and is no longer a working install path. New installs must use the official `/plugins` flow documented above. Invoking the legacy command exits with a usage error that names the v0.14.0 removal and points at the Codex `/plugins` flow.

`traceary integration codex uninstall` was retired in **v0.15.0** (#957). Both the install and uninstall surfaces are now hidden stubs that exit non-zero with a usage error pointing at Codex's official `/plugins` flow as the supported install/uninstall path. The retired uninstall command no longer appears in `traceary integration codex --help`.

### Manual cleanup for legacy installs

If you installed Traceary via the retired pre-v0.14 `traceary integration codex install` path, use Codex's official `/plugins` flow to uninstall the plugin first (run `codex` inside the repository, open `/plugins`, and uninstall `Traceary`). For state left behind by the retired Traceary-managed install, remove the residual files manually:

```sh
# Remove the legacy active plugin cache
rm -rf ~/.codex/plugins/cache/local-traceary-plugins/traceary

# Remove the legacy marketplace copy
rm -rf ~/.agents/plugins/plugins/traceary

# In ~/.agents/plugins/marketplace.json, remove the legacy plugin entry named
# "traceary" whose source path is "./plugins/traceary". If that local marketplace
# only contained Traceary, you can remove marketplace.json after deleting the copy.
# In ~/.codex/config.toml, delete the legacy [plugins."traceary@local-traceary-plugins"] table
# In ~/.codex/hooks.json, remove the named "traceary-session-start" / "traceary-session-stop" /
# "traceary-prompt" / "traceary-audit" entries. Leave [features].codex_hooks untouched so other
# local hook workflows keep working.
```

After cleanup, install via the official `/plugins` flow above so Codex itself manages the plugin lifecycle.
