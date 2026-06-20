# Antigravity hooks and plugin

[日本語](./antigravity.ja.md)

Antigravity is Google's successor to Gemini CLI as an AI agent host. As of **v0.21.1**, Traceary supports Antigravity as a real hook client with a packaged plugin, using only the documented public hook/plugin surface — no credential reads, no private app-internal formats, no browser automation.

> **What changed from v0.21.0:** v0.21.0 shipped Antigravity capability **diagnostics only** (`doctor --client antigravity` → `tool_unavailable`) and intentionally shipped no hook or package, because no public contract was confirmed at the time. Google has since published the public Antigravity hook/plugin/CLI surface, so v0.21.1 converts Antigravity from a diagnostic-only host into a supported hook client.

## What it wires automatically

Antigravity's `hooks.json` is a top-level map of *hook-group name* to event configs (Traceary owns the `traceary` group), which differs from the `{"hooks": {...}}` shape the other hosts share. Traceary renders and merges its own document for this host.

| Antigravity event | Traceary effect |
| --- | --- |
| `PreInvocation` | Idempotent session start/refresh keyed by `conversationId` (Antigravity has no `SessionStart`); the first workspace path becomes the workspace |
| `PreToolUse` (`run_command`) | Persists the proposed `{CommandLine, Cwd}` keyed by `conversationId + stepIdx`; never blocks (`{"decision":"allow"}`) |
| `PostToolUse` (`run_command`) | Pairs the command persisted by `PreToolUse` for the same step and records a `command_executed` audit (with the step `error`); fails soft when no pending command exists |
| `Stop` | Records the turn transcript from `transcriptPath` (best effort) and a turn boundary; **does not** close the session |

Antigravity payloads use camelCase fields (`conversationId`, `workspacePaths`, `transcriptPath`, `toolCall.name`, `toolCall.args.CommandLine`, `toolCall.args.Cwd`, `stepIdx`, `terminationReason`). Traceary normalizes these into its internal shape before reusing the shared session / audit / transcript runtime.

## Limitations

- **No `SessionStart`.** The earliest per-conversation signal is `PreInvocation`, which fires before every model call, so Traceary uses it as an idempotent session start/refresh keyed by `conversationId`.
- **`Stop` is a per-execution boundary, not a session end** (the same model as Codex — #1170). The session row stays open (memory auto-extract still fires) and ends only via MCP `manage_session` or stale GC (`traceary session gc`).
- **Only `run_command` tool calls are audited.** `PostToolUse` carries only `stepIdx`/`error`, not the command args; the args arrive on `PreToolUse`, so Traceary pairs the two across the step. Non-`run_command` tools record nothing.
- **Transcript extraction is best effort.** The documented `transcriptPath` file is `transcript.jsonl`, but its per-line schema is not part of the public hook contract, so the extractor leniently scans for assistant text/thinking blocks across several plausible JSONL shapes and silently skips otherwise.
- **No credential, keychain, cookie, or browser-storage reads.** Only the documented `transcriptPath` hook field is read from disk.

## Capture level in headless print mode (`agy --print`)

Headless `agy --print` runs capture **session start + run_command audit only**:

| Antigravity event | Headless `agy --print` |
| --- | --- |
| `PreInvocation` (session start) | ● fires — session start/refresh keyed by `conversationId` |
| `PreToolUse` + `PostToolUse` (`run_command`) | ● fires — command audit when the run uses `run_command` |
| `Stop` (transcript + turn boundary) | ✕ not emitted by the host in print mode |

In headless print mode the host does **not** emit a `Stop` (or any other
finalization) hook, so Traceary records no `transcript` event and no turn
boundary for that run. This was verified by dogfooding on 2026-06-20 (Traceary
0.21.2, `agy` 1.0.10): a clean `agy --print` smoke recorded `session_started`
(and `command_executed` when a `run_command` ran) but no `transcript` event. The
final transcript/turn boundary is captured **only** when the host emits `Stop`
with a `transcriptPath` — i.e. on interactive runs. This is the expected capture
level for print mode, not a Traceary install failure: `doctor --client antigravity`
still passes when the hooks are wired, because the four hooks are registered
correctly and the absence of a host `Stop` in print mode is a host-mode trait.

> Inspecting recorded events: hook-originated Antigravity events are stored with
> `client=hook` and `agent=antigravity`. Use `traceary list --agent antigravity`
> to read them. `traceary list --client antigravity` returns no rows for these
> events (it would only match events whose recorded client is literally
> `antigravity`). The `--client antigravity` selector on `doctor` / `hooks install`
> is unrelated — it selects which host's checks/config to run.

## Install

1. Install the Traceary CLI first.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Install the Traceary hooks for Antigravity.

```sh
# workspace-level install → <project>/.agents/hooks.json
traceary hooks install --client antigravity --project-dir .

# or user-level install → ~/.gemini/config/hooks.json
traceary hooks install --client antigravity --global
```

Aliases `agy` and `antigravity-cli` resolve to the same canonical `antigravity` client. The install is non-destructive: only the `traceary` hook group is replaced, and every other top-level hook group is preserved verbatim. Re-run with `--upgrade` to refresh the managed group while preserving user-added groups.

Alternatively, add the packaged plugin under [`integrations/antigravity-plugin/`](../../integrations/antigravity-plugin/), which ships the same `traceary` hook group as `hooks.json` plus a `plugin.json` manifest following the official Antigravity plugin schema.

## Setup guide

```sh
traceary hooks guide --client antigravity --project-dir .
```

This prints the install command, the doctor command, the expected config path, and the Antigravity-specific notes (PreInvocation session model, Stop turn boundary, run_command pairing).

## Doctor

```sh
traceary doctor --client antigravity --json
```

`doctor` reports the Antigravity capability plus one check per **hook install route**, because Antigravity supports three independent routes and any one of them is enough:

- `antigravity-capability` — `pass` when an Antigravity install is detected (the `agy`/`antigravity` CLI on PATH or the app bundle), since Traceary supports the public hooks/plugin contract and needs no Traceary-side authentication. It reports `not_installed` (warn) when neither the CLI nor the bundle is present. This check does not launch the app, perform browser automation, or read credentials.
- `antigravity-hooks-workspace` — the workspace route (`<project>/.agents/hooks.json`).
- `antigravity-hooks-user` — the user-level route (`~/.gemini/config/hooks.json`).
- `antigravity-cli-plugin` — the CLI plugin route directory `~/.gemini/antigravity-cli/plugins/traceary` that `agy plugin install` imports into. It `pass`es when the package uses the supported Antigravity top-level hook-group format and `warn`s when it finds a **stale Gemini-shaped package** — a legacy top-level `{"hooks": ...}` envelope or commands that call `traceary hook ... gemini`. The check reads only `plugin.json`, `hooks.json`, and `hooks/hooks.json`; it never reads transcripts or credentials.
- `antigravity-hooks` — the aggregate summary. It `fail`s when **any** route's config is malformed (a per-route `fail`), even if another route is healthy, because Antigravity rejects the bad config regardless; otherwise it `pass`es when **any** route is healthy and `warn`s with an actionable install message only when **no** route is healthy.

**Each route is optional on its own.** A missing route is reported as `skip`, never `warn`: for example, if the user-level or CLI-plugin route is healthy, the absent workspace `.agents/hooks.json` is `skip`ped and the `antigravity-hooks` summary stays `pass`. Doctor only warns about hook coverage when none of the three routes registers the `traceary` group. A route file that is present but malformed (not a JSON object) is reported as `fail`, since Antigravity itself rejects it regardless of the other routes.

Antigravity is not in the default doctor client list (`["claude","codex","gemini"]`); pass `--client antigravity` explicitly.

## Migrating a stale Gemini-imported plugin

If you previously imported the Traceary plugin through Gemini CLI, `~/.gemini/antigravity-cli/plugins/traceary` may still hold the **legacy Gemini shape**: a top-level `{"hooks": ...}` document whose commands call `traceary hook ... gemini`. In that state `agy plugin install` can report success without replacing the package, so Antigravity sessions stay wired to the Gemini hook runtime instead of the Antigravity one. The supported package instead uses a top-level hook-group document with a `traceary` group invoking `traceary hook antigravity ...`.

`traceary doctor --client antigravity` surfaces this as the `antigravity-cli-plugin` warning. To remediate, remove the stale directory and reinstall the supported package:

```sh
rm -rf ~/.gemini/antigravity-cli/plugins/traceary
agy plugin install integrations/antigravity-plugin
# or wire hooks directly without the CLI plugin:
traceary hooks install --client antigravity --upgrade
```

Re-run `traceary doctor --client antigravity` to confirm the check flips to `pass`.

## Local discovery

The following was observed in the local development environment:

| Property | Value |
| --- | --- |
| Application path | `/Applications/Antigravity.app` |
| Bundle ID | `com.google.antigravity` |
| URL scheme | `antigravity://` |
| Workspace hooks path | `<project>/.agents/hooks.json` |
| Global hooks path | `~/.gemini/config/hooks.json` |

## Package validation

```sh
agy plugin validate integrations/antigravity-plugin
# structural validation in-repo:
go run ./cmd/repo-tooling integrations verify
```

## Official references

Verified 2026-06-20 JST:

- Antigravity 2.0 hooks: https://antigravity.google/assets/docs/antigravity-2-0/hooks.md
- Antigravity IDE hooks: https://antigravity.google/assets/docs/editor/ide-hooks.md
- Antigravity CLI plugins: https://antigravity.google/assets/docs/cli/cli-plugins.md
- Antigravity 2.0 plugins: https://antigravity.google/assets/docs/antigravity-2-0/plugins.md
- Antigravity IDE plugins: https://antigravity.google/assets/docs/editor/ide-plugins.md
- Antigravity CLI install: https://antigravity.google/assets/docs/cli/cli-install.md

If you are migrating from Gemini CLI, the [Gemini CLI extension](./gemini-extension.md) remains available for existing Gemini CLI installs.
