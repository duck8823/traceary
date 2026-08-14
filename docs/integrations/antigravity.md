# Antigravity hooks and plugin

[日本語](./antigravity.ja.md)

Antigravity is Google's successor to Gemini CLI as an AI agent host. As of **v0.21.1**, Traceary supports Antigravity as a real hook client with a packaged plugin, using only the documented public hook/plugin surface — no credential reads, no private app-internal formats, no browser automation.

> **What changed from v0.21.0:** v0.21.0 shipped Antigravity capability **diagnostics only** (`doctor --client antigravity` → `tool_unavailable`) and intentionally shipped no hook or package, because no public contract was confirmed at the time. Google has since published the public Antigravity hook/plugin/CLI surface, so v0.21.1 converts Antigravity from a diagnostic-only host into a supported hook client.

## What it wires automatically

Antigravity's `hooks.json` is a top-level map of *hook-group name* to event configs (Traceary owns the `traceary` group), which differs from the `{"hooks": {...}}` shape the other hosts share. Traceary renders and merges its own document for this host.

| Antigravity event | Traceary effect |
| --- | --- |
| `PreInvocation` | Idempotent session start/refresh keyed by `conversationId` (Antigravity has no `SessionStart`); the first workspace path becomes the workspace; on the first eligible firing, injects recalled summaries through `injectSteps` |
| `PreToolUse` (`run_command`) | Persists the proposed `{CommandLine, Cwd}` keyed by `conversationId + stepIdx`; never blocks (`{"decision":"allow"}`) |
| `PostToolUse` (`run_command`) | Pairs the command persisted by `PreToolUse` for the same step and records a `command_executed` audit (with the step `error`); fails soft when no pending command exists |
| `Stop` | Recovers the latest user prompt and model response from `transcriptPath`, records `prompt` + `transcript`, then records a turn boundary; **does not** close the session |

Antigravity payloads use camelCase fields (`conversationId`, `workspacePaths`, `transcriptPath`, `toolCall.name`, `toolCall.args.CommandLine`, `toolCall.args.Cwd`, `stepIdx`, `terminationReason`). Traceary normalizes these into its internal shape before reusing the shared session / audit / transcript runtime.

### Hook output vocabulary

Antigravity parses hook stdout as JSON. The output vocabulary used by Traceary is:

- `PreInvocation`: `{ "injectSteps": [...] }`, where each step may be a `toolCall`, `userMessage`, or `ephemeralMessage`. Traceary uses one `ephemeralMessage` for recalled wake summaries, not `userMessage`, because the text is not something the user typed.
- `PreToolUse`: `{ "decision": "allow" }`.
- `Stop`: `{ "decision": "" }`.

The `PreInvocation` contract was confirmed in the vendor documentation shipped with the Antigravity CLI at `~/.gemini/antigravity-cli/builtin/skills/agy-customizations/docs/hooks.md` (the public contract is also documented at [Antigravity hooks](https://antigravity.google/docs/hooks)). Its contract example uses `injectSteps` with `ephemeralMessage`; the matcher is ignored, handlers are a flat list, and the default handler timeout is 30 seconds.

The packaged plugin includes the four shared skills (`traceary-session-history`, `traceary-session-refine`, `traceary-memory-review`, `traceary-memory-remember`; see [skills](./skills.md)). Skills route agents through the Traceary CLI. Direct `traceary hooks install` routes install hooks only; use the packaged plugin when Antigravity should discover skills automatically. The Traceary MCP server declaration was removed in v0.35.0 (#1871).

## Usage metadata from the status line

Antigravity's hook payloads do not expose provider usage. Its separate status-line payload does expose body-free cumulative totals. Traceary can consume that payload through the internal composition command:

```sh
traceary hook antigravity statusline
```

Do not configure that command as the only status-line renderer: it intentionally writes no display output. Compose it with an existing renderer, for example in Bash:

```bash
#!/usr/bin/env bash
tee >(traceary hook antigravity statusline >/dev/null 2>&1) |
  "$HOME/.local/bin/my-antigravity-statusline"
```

Configure this wrapper in `~/.gemini/antigravity-cli/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.gemini/antigravity-cli/traceary-statusline.sh"
  }
}
```

This is opt-in and is not modified by `traceary hooks install`.

Traceary accepts only `idle` payloads and stores `total_input_tokens` and `total_output_tokens` as the latest immutable snapshot for the conversation/model source. Older snapshots are superseded rather than added. It ignores `current_usage`, cache counters, quota, email, account fields, and cost. A repeated snapshot is idempotent; a cumulative regression fails closed. When a `Stop` transcript exposes a stable completed-turn step, that uncorrelatable boundary is stored separately as usage **unavailable**. If no stable step exists, Traceary does not invent a call identity. It never estimates usage from transcript length or command count.

## Limitations

- **No `SessionStart`.** The earliest per-conversation signal is `PreInvocation`, which fires before every model call, so Traceary uses it as an idempotent session start/refresh keyed by `conversationId`.
- **`Stop` is a per-execution boundary, not a session end** (the same model as Codex — #1170). The session row stays open (memory auto-extract still fires) and ends only via `traceary session end` or stale GC (`traceary session gc`).
- **Only `run_command` tool calls are audited.** `PostToolUse` carries only `stepIdx`/`error`, not the command args; the args arrive on `PreToolUse`, so Traceary pairs the two across the step. Non-`run_command` tools record nothing.
- **Prompt text is not a direct hook field.** The public hook payload exposes `transcriptPath`; Traceary recovers the latest `USER_INPUT` / `USER_EXPLICIT` row from that file at Stop.
- **Transcript extraction is best effort.** The documented `transcriptPath` file is `transcript.jsonl`. Traceary supports the current CLI `MODEL` / `*_RESPONSE` rows plus legacy nested/flat shapes, preserving separate thinking/text blocks, and silently skips unknown shapes.
- **No credential, keychain, cookie, or browser-storage reads.** Only the documented `transcriptPath` hook field is read from disk.
- **Status-line capture is partial and opt-in.** It provides cumulative conversation/model totals, not provider-call identity. Traceary does not correlate a status snapshot with `Stop`.

## Capture level in headless print mode (`agy --print`)

Current Antigravity hooks expose the same lifecycle signals in headless and interactive runs:

| Antigravity event | Capture level | Headless `agy --print` | Interactive |
| --- | --- | --- | --- |
| `PreInvocation` (session start) | `start_supported` | ● session start/refresh keyed by `conversationId` | ● |
| `PreToolUse` + `PostToolUse` (`run_command`) | `tool_audit_supported` | ● when `run_command` is used | ● |
| `Stop` (`prompt` + `transcript` + turn boundary) | `final_turn_supported` | ● current CLI emits Stop with `transcriptPath` | ● |

This was re-verified on 2026-07-13 against `agy` 1.1.1 and the current official hook contract. The public payload does not carry prompt text directly, but every hook receives `transcriptPath`; Traceary reads the latest explicit user input and model response from that file at Stop. A healthy hook configuration still does not prove that files were readable or events persisted, so `antigravity-event-coverage` checks recent database evidence and warns when transcript coverage falls below the configured threshold.

When `workspacePaths` is empty (observed on `agy` 1.1.x for some untrusted or headless runs), Traceary recovers the project workspace from the Antigravity host process cwd chain instead of the hook process cwd (which is the `hooks.json` directory). Without that fallback, events would be stored with an empty workspace and would not appear under the default `traceary list` workspace filter even though hooks delivered successfully.

> Inspecting recorded events: hook-originated Antigravity events are stored with
> `client=hook` and `agent=antigravity`. Use `traceary list --agent antigravity`
> to read them. `traceary list --client antigravity` returns no rows for these
> events (it would only match events whose recorded client is literally
> `antigravity`). The `--client antigravity` selector on `doctor` / `hooks install`
> is unrelated — it selects which host's checks/config to run.

## Scoped hook permissions for sandboxed headless runs

Antigravity evaluates command hooks through its permission engine. Interactive
runs can ask the operator, but non-interactive `agy --print` cannot answer that
prompt. Hook files can therefore be installed correctly while the hooks remain
non-executable in headless mode.

The plugin packages a directly mergeable settings fragment at
[`integrations/antigravity-plugin/permissions.example.json`](../../integrations/antigravity-plugin/permissions.example.json):

```json
{
  "permissions": {
    "allow": [
      "command(traceary hook antigravity pre-invocation)",
      "command(traceary hook antigravity pre-tool-use)",
      "command(traceary hook antigravity post-tool-use)",
      "command(traceary hook antigravity stop)"
    ]
  }
}
```

Merge those four entries into the `permissions.allow` array in Antigravity
CLI's global `~/.gemini/antigravity-cli/settings.json`. Do not place them only
in `~/.gemini/settings.json` or a project settings file: Antigravity CLI 1.1.6
does not use those documents as this integration's headless permission source,
and `doctor` therefore reports that configuration as not ready. The plugin
intentionally does **not** overwrite operator-owned settings during
installation.

These are exact token-prefix resources for the four packaged hook entrypoints.
Do not replace them with a wildcard command grant, an `unsandboxed(...)` grant,
`--dangerously-skip-permissions`, or a run without `--sandbox`. Also check the
effective `deny` and `ask` lists: Antigravity evaluates **deny > ask > allow**,
so an explicit broad ask rule can shadow these exact allows and make headless
execution wait for an impossible prompt.

Register the `traceary` hook group through **exactly one** route: workspace,
user-level, or CLI plugin. More than one active route can invoke the same
lifecycle handler twice. `doctor` reports multiple healthy routes as a warning
instead of treating duplicate registration as healthy.

## Body-free headless marker probe

After adding the scoped permission fragment, run:

```sh
scripts/verify-antigravity-headless-markers.sh
```

The probe builds the candidate binary, puts it first on `PATH`, keeps
`--mode plan --sandbox`, and uses an isolated temporary Traceary database. If
`agy` auto-denies a hook (often exit 0, empty stdout, permission wording on
stderr), the probe reports a scoped-permission failure instead of a missing
marker. `traceary doctor --client antigravity` reports the same condition as
`antigravity-headless-hooks`. It
verifies a fixed public response marker and reads back only
`id,kind,session,source_hook`; it never prints or copies prompt, response, or
transcript bodies. A healthy current host reports `session_start`, `prompt`,
`final_turn`, and `stop_boundary` as `true`. `session_end` remains `false`
because Antigravity `Stop` is a turn boundary rather than a true session-end
event.

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

Alternatively, install the packaged plugin under [`integrations/antigravity-plugin/`](../../integrations/antigravity-plugin/). It ships the same `traceary` hook group, a versioned `plugin.json` manifest following the official Antigravity schema, the four shared skills (see [skills](./skills.md)), and the opt-in `permissions.example.json` fragment. Do not also retain a direct workspace or user-level Traceary hook route.

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
- `antigravity-cli-plugin` — the current shared plugin directory `~/.gemini/config/plugins/traceary` plus the legacy CLI-specific directory `~/.gemini/antigravity-cli/plugins/traceary`. It `pass`es when the package uses the supported Antigravity top-level hook-group format and `warn`s when it finds a **stale Gemini-shaped package** — a legacy top-level `{"hooks": ...}` envelope or commands that call `traceary hook ... gemini`. The check reads only `plugin.json`, `hooks.json`, and `hooks/hooks.json`; it never reads transcripts or credentials.
- `antigravity-hooks` — the aggregate summary. It `fail`s when **any** route's config is malformed (a per-route `fail`), even if another route is healthy, because Antigravity rejects the bad config regardless. It `pass`es when **exactly one** route is healthy, `warn`s when multiple routes are healthy because they can register duplicate handlers, and also `warn`s with an actionable install message when no route is healthy.
- `antigravity-headless-hooks` — distinguishes installed hook files from executable non-interactive coverage. It `pass`es only when **exactly one** route is healthy and the global `~/.gemini/antigravity-cli/settings.json` allows all four exact command resources without a matching deny/ask rule or a broader/unsandboxed grant. It `warn`s when multiple routes could duplicate hooks, when installed hooks would still prompt or are shadowed, or when permissions exist only in Gemini/project settings. It `skip`s when no healthy route exists.
- `antigravity-capture-levels` — always `pass`. Reports the configured public hook capabilities: `start_supported`, `tool_audit_supported`, and `final_turn_supported` for interactive and current headless CLI runs.
- `antigravity-event-coverage` — checks recent `agent=antigravity` database evidence. It warns when a sufficient sample of started sessions lacks transcript events, even if all hook install routes are healthy.
- `antigravity-plugin-version` — compares the installed plugin manifest version with the running Traceary release and warns when they differ. Reinstall the packaged plugin after upgrading Traceary.

**Each route is optional on its own, but only one should be active.** A missing route is reported as `skip`, never `warn`: for example, if the user-level route is healthy, the absent workspace `.agents/hooks.json` and CLI plugin are `skip`ped and the `antigravity-hooks` summary stays `pass`. Multiple healthy routes warn about duplicate registration. A route file that is present but malformed (not a JSON object) is reported as `fail`, since Antigravity itself rejects it regardless of the other routes.

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

The Antigravity validator should report `3 processed` skills and `1 processed` hook group. The packaged plugin no longer declares a Traceary MCP server (#1871).

## Official references

Verified 2026-07-24 JST against Antigravity CLI 1.1.6:

- Antigravity permissions and precedence: https://antigravity.google/docs/cli-permissions
- Antigravity hooks and handler contract: https://antigravity.google/docs/hooks
- Antigravity plugins: https://antigravity.google/docs/plugins
- Antigravity 2.0 hooks: https://antigravity.google/assets/docs/antigravity-2-0/hooks.md
- Antigravity IDE hooks: https://antigravity.google/assets/docs/editor/ide-hooks.md
- Antigravity CLI plugins: https://antigravity.google/assets/docs/cli/cli-plugins.md
- Antigravity 2.0 plugins: https://antigravity.google/assets/docs/antigravity-2-0/plugins.md
- Antigravity IDE plugins: https://antigravity.google/assets/docs/editor/ide-plugins.md
- Antigravity CLI install: https://antigravity.google/assets/docs/cli/cli-install.md

If you are migrating from Gemini CLI, the [Gemini CLI extension](./gemini-extension.md) remains available for existing Gemini CLI installs.
