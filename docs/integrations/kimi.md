# Kimi Code plugin

[日本語](./kimi.ja.md)

Traceary v0.29.0 adds a native Kimi Code integration. The package under
[`integrations/kimi-plugin/`](../../integrations/kimi-plugin/) installs ten
live-verified lifecycle hooks, one local Traceary MCP server, and the three
shared memory/session skills through a single `kimi.plugin.json` manifest.
Recorded hook events use `client=hook` and `agent=kimi`.

## Supported coverage

The package targets the live-verified Kimi Code 0.27.0 contract
([machine-readable contract](../hooks/host-contract.json)).

| Kimi Code event | Traceary behavior |
| --- | --- |
| `SessionStart` | Starts the native Kimi session; `source=resume` re-fires with the same session id and is recorded idempotently |
| `SessionEnd` | Ends the session (`reason=exit`) |
| `UserPromptSubmit` | Records the user prompt (content-block array flattened to text) |
| `PreToolUse` (`matcher = "Agent"`) | Starts the correlated child session for a subagent |
| `PostToolUse` | Records one completed command audit (`tool_output` captured) |
| `PostToolUseFailure` | Records the failed audit with the flattened `error{code,message,retryable}` detail |
| `Stop` | Records the assistant transcript best-effort from the session wire log (`session_index.jsonl` → `agents/main/wire.jsonl`); it is a turn boundary, not a session end |
| `SubagentStop` | Ends the active child session (latest-active fallback, same semantics as Claude) |
| `PreCompact` / `PostCompact` | Records compact markers (`trigger` = auto/manual); Kimi exposes token counts but no summary body |

`Notification`, `Interrupt`, `StopFailure`, `PermissionRequest`, and
`PermissionResult` are not wired: they were not live-observed or carry no
Traceary lifecycle mapping. The dedicated `SubagentStart` event is also
unwired — it has no correlation ids, so the Agent tool's `PreToolUse` is the
start signal instead. See the [host coverage matrix](../hooks/host-coverage.md)
for the field-level status.

## Install

1. Install the Traceary CLI and confirm that `traceary` is on `PATH` (the
   plugin hooks and the MCP server invoke it).

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

2. From a matching Traceary release tag checkout, install the plugin:

```sh
./scripts/install-kimi-plugin.sh
```

The installer mirrors the official local install: it stages the package as a
generation directory under `~/.kimi-code/plugins/managed/`, flips the
`traceary` symlink atomically, and upserts the `plugins/installed.json`
record while preserving your `enabled` state. Re-running is idempotent.

Alternatively, use Kimi Code's official flow: `/plugins install <path-or-url>`
inside Kimi Code, pointing at `integrations/kimi-plugin/` or this repository.

3. Start a new session (or `/plugins reload`), then verify:

```sh
traceary doctor --client kimi
```

A healthy install reports `kimi-cli`, `kimi-plugin`, `kimi-hooks`, `kimi-mcp`,
and `kimi-skills` all passing. Kimi is an opt-in doctor client, so plain
`traceary doctor` (claude/codex/gemini) is unchanged.

## Manual hook configuration (alternative)

If you prefer not to install the plugin, append the generated TOML rules to
`~/.kimi-code/config.toml` yourself:

```sh
traceary hooks print --client kimi
```

`traceary hooks install --client kimi` intentionally stays fail-closed:
Traceary does not merge TOML into your config.

## Troubleshooting

- **Hooks fire but nothing is recorded**: the plugin invokes `traceary` from
  `PATH`. Make sure the kimi-aware Traceary binary (v0.29.0+) comes first on
  `PATH`; `traceary doctor` reports PATH mismatches.
- **No transcript events**: transcripts are recovered best-effort from the
  session wire log. Older sessions without a `session_index.jsonl` entry are
  skipped silently by design.
- **Plugin shows as installed but hooks do not fire**: check
  `~/.kimi-code/plugins/installed.json` has the `traceary` entry with
  `"enabled": true` and `"state": "ok"`, then `/plugins reload`.
- **Upgrade drift**: `traceary doctor --client kimi` warns when the managed
  plugin version does not match the running Traceary binary; reinstall with
  `scripts/install-kimi-plugin.sh` from a matching release tag.
