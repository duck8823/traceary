# Muse Code plugin

[日本語](./muse.ja.md)

Traceary v0.50.0 adds a native Muse Code integration. The package under
[`integrations/muse-plugin/`](../../integrations/muse-plugin/) declares eight
lifecycle hooks, one local Traceary CLI, and the four shared skills (see
[skills](./skills.md)). Recorded hook events use `client=hook` and
`agent=muse`. Host pin: **Muse Code 1.0.3**. There is no live session corpus
yet, so doctor session enrichment stays off.

## Install

There is no `scripts/install-muse-plugin.sh` in this slice. Install the
packaged directory through Muse's plugin flow, then confirm:

```sh
muse plugins validate integrations/muse-plugin --json
muse plugins list --json
traceary doctor --client muse --json
```

`muse plugins list --json` was evidenced as `{"plugins":[]}` with no plugin
installed (#2350 §4). A non-empty list shape is not a merge gate; doctor
treats unrecognized JSON as a WARN, not a FAIL.

## Headless runs

Default Muse approval and sandbox are **on**. For unattended `muse exec`,
use one of:

```sh
muse exec --disable-approval -- ...
muse exec --approval-mode never -- ...
muse exec --yolo -- ...
```

Stall signature: `approval_wait.effect.started` in `session.jsonl` without a
matching `approval_wait.effect.terminal` (#2350 §2). Do not treat `--json`
stdout as a hook substitute.

## Supported coverage

The eight packaged hooks (#2352 runtime, `integrations/muse-plugin/hooks/hooks.json`):

| Muse event | Traceary action | Matrix cell |
| --- | --- | --- |
| `SessionStart` | `traceary hook muse session-start` | `session_started` **wired** |
| `UserPromptSubmit` | `traceary hook muse user-prompt-submit` | `prompt` **wired** |
| `PreToolUse` | `traceary hook muse pre-tool-use` | validation only |
| `PostToolUse` | `traceary hook muse post-tool-use` | `command_executed` **available** (declared; no live tool-dispatch capture probe) |
| `PostToolUseFailure` | `traceary hook muse post-tool-use-failure` | `command_executed` **available** (same) |
| `Stop` | `traceary hook muse stop` | `transcript` **wired** (turn boundary, not session end) |
| `PreCompact` | `traceary hook muse pre-compact` | `compact_summary` **available** (declared; dispatch unobserved on exec/TUI/resume) |
| `PostCompact` | `traceary hook muse post-compact` | `compact_summary` **available** (same) |

Honest caveats (copied from the Muse matrix cells):

- **`session_ended` available** — `SessionEnd` exists in binary `HookEventKind`,
  but packaged `hooks.json` does not subscribe. Durable `session.end` is a
  log event, not hook proof (#2350 findings §4).
- **`consolidation_request` unsupported** — no usable host signal; Stop-exit-2
  equivalent is explicitly unknown (#2350 §4).
- **`command_executed` / `compact_summary` available** — declared in
  `hooks.json`, not a wired capture claim until live dispatch is observed.

See the [host coverage matrix](../hooks/host-coverage.md). Verify with
`traceary doctor --client muse` (`muse-cli` / `muse-plugin` / `muse-hooks` /
`muse-skills`). `muse-hooks` stays WARN while SessionEnd is unsubscribed and
compact/tool dispatch is unobserved.

## Spool replay

Deferred Muse hooks persist with command `muse`. Doctor spool drain replays
the eight actions above through `replayMuseSpoolRecord` (same pattern as
Kimi/Grok). A missing replay case would fail as
`unsupported hook spool command: muse`.
