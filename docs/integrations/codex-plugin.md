# Codex plugin

[日本語](./codex-plugin.ja.md)

Traceary ships a Codex plugin under `plugins/traceary/` that plugs into Codex CLI's official `/plugins` flow.
Codex picks up the MCP server, slash commands, and session-history skill as soon as the plugin is installed through the official flow. Plugin hooks require one additional security step: Codex skips non-managed hooks until the user reviews and trusts their current definition. Open `/hooks` after installation (and after any plugin update that changes a hook), review the Traceary entries, and trust them. `traceary doctor --client codex` checks the effective trust state through Codex and warns when a hook is untrusted, modified, or disabled.

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

4. Inside Codex, open `/plugins`, choose `Traceary Plugins` as the marketplace, and install the `Traceary` plugin. Codex materializes the plugin into its managed cache and discovers the hooks declared in the plugin manifest.

5. Open `/hooks`, review the Traceary plugin hook commands, and trust the current definitions. Installation alone does not trust plugin-bundled hooks; changed definitions require review again.

6. Open a new thread and verify:

```sh
traceary doctor --client codex --json
```

## What the official flow wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `Stop` (body-free usage plus turn-boundary transcript; not a session end — #1170), and `PostToolUse` hooks (declared in `plugins/traceary/hooks.json` and referenced from the plugin manifest) — **only when `plugin_hooks` is enabled on your Codex build and the current definitions are trusted in `/hooks`**; otherwise see the fallback below
- slash commands: `/traceary:help` and `/traceary:doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて").

## Hook trust diagnostics and legacy fallback

Current Codex builds expose the effective hook state through `/hooks`: `trusted` hooks run, while `untrusted`, changed (`modified`), and disabled hooks do not. Traceary delegates the current-definition hash comparison to Codex instead of attempting to reproduce Codex's private hash algorithm.

Diagnose with:

```sh
traceary doctor --client codex --json        # checks effective plugin hook trust
codex                                        # open /hooks and review Traceary entries
```

If an older Codex build cannot expose plugin hook state, doctor reports the state as unverified rather than passing it. Upgrade Codex first. The manual registration remains a compatibility fallback when plugin-managed hooks genuinely cannot be loaded:

```sh
traceary hooks install --client codex --upgrade --traceary-bin "$(command -v traceary)"
traceary doctor --client codex --json
```

The fallback writes Traceary-managed entries directly into `~/.codex/hooks.json` (named `traceary-session-start`, `traceary-prompt`, `traceary-usage`, `traceary-transcript`, `traceary-session-stop`, `traceary-audit`). Existing non-Traceary entries are preserved.

## Verified usage capture

On each trusted interactive Codex `Stop`, Traceary reads the matching local rollout JSONL under `CODEX_HOME/sessions` (or `~/.codex/sessions`). A turn begins at `turn_context` and ends at the matching `task_complete` or `turn_aborted`. Traceary subtracts the last cumulative `token_count.info.total_token_usage` before the turn from the final cumulative snapshot at the terminal boundary. Intermediate and compaction snapshots replace earlier snapshots; they are never added together. A missing baseline or terminal snapshot, an ambiguous boundary, or any counter regression within the segment makes the turn an excluded `unavailable` observation. A terminal without a snapshot also invalidates attribution for the following turn; that following terminal snapshot can establish a fresh baseline only for later turns.

For `traceary session run -- codex exec --json ...`, capture mode is fixed to `headless_stream`. Traceary forwards stdout unchanged and retains only `thread.started.thread_id` and terminal `turn.completed.usage` in memory. Headless and rollout samples derive the same body-free `(thread_id, turn ordinal)` exclusivity key. SQLite atomically gives the first persisted sample the single additive claim and retains any later or concurrent alternative as excluded evidence, preventing double counting regardless of delivery order.

- Every terminal turn has a deterministic body-free observation ID, so duplicate hooks, retries, and resumed sessions replay without adding tokens twice.
- Known zero remains known zero. A field omitted by Codex is stored as `unavailable`, not numeric zero.
- If a stable Stop `event_id` has no readable usage record, Traceary stores one excluded observation with explicitly unavailable counters. If the host provides neither usage nor a stable delivery ID, Traceary skips instead of inventing identity from content.
- The reader rejects symlinks in every matched path component, verifies the opened regular file is the inspected file, and enforces hard file/read and JSONL-line size limits.
- Durable retry spools for the usage hook contain only `session_id` and a verified `event_id`; assistant text and other Stop payload fields are discarded before the spool is written.

The capture is local-only. It does not send the rollout or usage record to a network service and does not estimate billing cost.

### Duplicate-capture warning

If trusted plugin hooks and these manual entries are both active, **every session/prompt/transcript/audit event will be recorded twice**. Let doctor detect and remove the obsolete manual path:

```sh
traceary doctor --client codex --json
traceary doctor --fix --dry-run --client codex
traceary doctor --fix --client codex
```

Doctor only offers this cleanup when Codex reports the current Traceary plugin hook definitions as trusted. It removes the named Traceary-managed entries (`traceary-session-start`, `traceary-prompt`, `traceary-usage`, `traceary-transcript`, `traceary-session-stop`, and `traceary-audit`) while preserving unrelated hooks and top-level fields. When trust is unverified, untrusted, modified, or disabled, doctor keeps the manual fallback intact.

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

`traceary integration codex install` was retired in **v0.14.0** (#920). `traceary integration codex uninstall` was retired in **v0.15.0** (#957). The entire `traceary integration` command tree was **fully removed in v0.25.0** (#1266); invocations fail as unknown commands. New installs and uninstalls must use the official Codex `/plugins` flow documented above.

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
