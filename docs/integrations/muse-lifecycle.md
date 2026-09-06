# Muse Code lifecycle (investigation)

[日本語](./muse-lifecycle.ja.md)

Investigation for [#2350](https://github.com/duck8823/traceary/issues/2350)
(`v0.50.0-1`). Evidence-only: **no Muse plugin and no host-coverage matrix
changes** in this issue. Those belong to `v0.50.0-2` / `v0.50.0-3`.

Pinned CLI (2026-09-06):

| Item | Observed |
| --- | --- |
| `muse --version` | `Muse Code 1.0.3 (1.0.3-R2198.1)` |
| Launcher | `/Users/duck8823/.local/bin/muse` (bash wrapper, `channel=muse-stable`) |
| Runtime binary | `/Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1` (`Mach-O 64-bit executable arm64`) |
| Data root | `~/.local/share/muse/` |
| Config root | `~/.config/muse/` |

Commands:

```sh
muse --version
ls -la "$(command -v muse)"
ls -la /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
file /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
```

---

## 1. Session lifecycle

### Start

- Interactive: `muse [OPTIONS] [PROMPT]` opens the TUI (`muse --help`: “If no
  subcommand is given, options run the interactive TUI”).
- Headless: `muse exec [OPTIONS] [PROMPT]` runs **one prompt** and exits
  (`muse exec --help`: “Run one prompt non-interactively (headless)”).
- `muse exec --json` emits MSP JSONL on stdout. Observed payload types on an
  echo-provider probe (`--provider echo --no-session-log`):
  `runtime.command.accepted`, `session.run.linked`,
  `session.workspace_branch.observed`, `turn.input.user`,
  `run.lifecycle.started`, `task.stream.linked`, `task.lifecycle.*`,
  `run.output.delta`, `run.terminal.completed`. Exit `0`.
- Durable session logs (when `--no-session-log` is **not** set) also record
  `session.opened.observed` (`resume: false`), `session.startup_phases.observed`,
  and later `session.end`. Sample from
  `~/.local/share/muse/sessions/2026/09/03/01a067b6-81b6-7490-91b0-494857e673a1/session.jsonl`:
  - `session.opened.observed` → `resume: false`, `security_mode: "normal"`
  - `session.end` → `exit_reason: "clean"`, `uptime_ms`, `session_log_bytes`

`--no-session-log` skips disk persist and disables local session messaging:

```
muse: local session messaging disabled: session logging is required
```

(echo `exec` probe, 2026-09-06.)

### Persist

Layout (read-only inspection):

```
~/.local/share/muse/sessions/YYYY/MM/DD/<session-id>/
  session.jsonl          # durable event log
  cli-*.log              # per-process CLI log
  cron.db[+shm|+wal]     # session-local cron
  session.peer-history.sqlite3
  tool-outputs/.spool/   # tool output spool (e.g. call_*-bash.txt.tmp)
  approval-review/       # often empty until a review artifact is written
  .session.lock
```

Index: `~/.local/share/muse/session-index.db` table `sessions`
(`session_id`, `session_dir`, `session_log_path`, `workspace_root`, `status`,
`layout`, titles, timestamps).

A completed `muse exec` (meta provider, `--approval-mode on-request`,
session `01a07613-1043-75a2-b471-661929150803`) wrote
`sessions/2026/09/06/<id>/session.jsonl` (95 lines) plus
`sessions/.msp-view-v1/<id>`.

### Resume

```
muse resume            # session picker for this workspace
muse resume --last     # most recent session in this workspace
muse resume <uuid>
```

(`muse resume --help`.) Resume is **interactive TUI**, not a headless one-shot.
Durable log records `session.resumed` (sample:
`prior_turn_count: 3`, `resumed_from_sequence: 811` on session
`01a0692b-a8e2-7530-8729-a67260ea9f19`). That same resume sample’s
`session.started` used `"background_tasks": "kill"` plus
`previous_session_stream`. `"background_tasks": "keep"` was observed on a
**different** session (`01a067b6-81b6-7490-91b0-494857e673a1`, 2026/09/03),
not on the resume sample. Local corpus grep of
`~/.local/share/muse/sessions` found 3× `"keep"` and 3× `"kill"`.
**Unknown:** other enum values, when Muse chooses keep vs kill, and
headless semantics.

`muse exec` has `--session-id <UUID>` (use a specific id for a **new**
headless run). **Unknown:** whether `exec --session-id` of an *existing*
id continues that session’s transcript the way `muse resume` does. Not
live-probed.

### Headless vs interactive (observed)

| | Interactive (`muse` / `muse resume`) | Headless (`muse exec`) |
| --- | --- | --- |
| UI | TUI; resume opens a picker unless `--last` / uuid | No TUI; one prompt then exit |
| Machine output | not claimed | `--json` JSONL on stdout |
| Approval | operator can answer in TUI | no TTY approval UI; waits or auto-decides from flags |
| Extra exec flags | (root flags still apply) | `--max-model-steps`, `--prompt-file`, `--user-input-auto-resolve`, compaction thresholds, `--allow-workspace-switch` |
| Session log | default on | default on; `--no-session-log` available on both |

---

## 2. Approval / sandbox in non-interactive runs

Safety is **on by default** (`muse --help` / `muse exec --help`):

- `--approval-mode untrusted|on-request|never` (default **`on-request`**)
- `--approval-judge off|on` (default **on**)
- `--yolo` — disable approval **and** sandbox and trust the workspace (this run)
- `--disable-approval` — disable tool approval prompts for this run
- `--disable-sandbox` / `--sandbox-network` / `--trust-workspace`

### Stall signature (reproduced 2026-09-06)

Command (process killed after ~70s with SIGTERM):

```sh
muse exec --provider meta --trust-workspace --max-model-steps 8 --json \
  --approval-mode untrusted --approval-judge off \
  "Run exactly this shell command and nothing else: echo 2350-stall-probe. ..."
```

- Process stayed alive (`poll None`) until SIGTERM (`returncode 143`).
- Stderr: `workspace trust: trusted source=run-flag` then
  `received SIGTERM; flushed session logs`.
- `--json` stdout **did not** include `approval_wait.*` while waiting.
- Durable `session.jsonl` for
  `01a07613-befd-7222-b513-29ac4d8a4f92` **did**:
  - `runtime.session` `kind=approval` `event.kind=requested`
  - `approval_wait.effect.started` (`pending_action_id` present)
  - after SIGTERM: `event.kind=decision_applied` `decision=abort`
    `policy_result=deny`, `approval_wait.effect.terminal`
    `outcome.kind=cancelled`, `session.end` `exit_reason=clean`

Historical unmatched waits (started without terminal) exist in several
`2026/09/04` and `2026/09/05` session logs (same `approval_wait.effect.started`
payload type).

**Zero child processes:** `pgrep -P <muse-bin-exec-pid>` is often empty
*both* while waiting on approval *and* while waiting on the model. A live
`muse exec ... --approval-mode never` (pid 73013, 2026-09-06) also had
**zero children** at snapshot time. Treat “zero children” as supporting
evidence **together with** unmatched `approval_wait.effect.started` in
`session.jsonl`, not as a standalone stall detector.

**Unknown:** whether `--json` ever emits `approval_wait.*` on a later
CLI build. On 1.0.3-R2198.1 it was session-log-only in this probe.

### Flag remedy

Use one of:

```sh
muse exec --yolo -- ...
muse exec --disable-approval -- ...
muse exec --approval-mode never -- ...
```

`--yolo` also disables the sandbox and trusts the workspace. Prefer
`--disable-approval` or `--approval-mode never` when sandbox should stay on.

`--approval-mode on-request` with `--trust-workspace` **did not stall** on a
simple `echo` tool call (session `01a07613-1043-...` completed with
`run.terminal.completed` / `tool.result` in ~16s). Default `on-request` plus
the LLM approval judge can auto-allow some tools; **`untrusted` +
`--approval-judge off`** is the reliable stall reproduction.

Orchestration already in the wild (ps, 2026-09-06):

```
muse-bin-1.0.3-R2198.1 exec --provider meta ... --trust-workspace \
  --approval-mode never --no-session-log --max-model-steps 120 ...
```

---

## 3. Background-task model and artifacts

Observed (not a complete product spec):

- In-session **task streams**: `task.stream.linked`, `task.lifecycle.proposed|
  accepted|scheduled|side_effect_intent|started|status|completed` on `exec --json`.
- Session-local **`cron.db`**: Muse documents `cron_create` in the binary
  (5-field local cron, 7-day expiry). Files sit beside `session.jsonl`.
- `muse session-message` — list/send cross-session messages (not exercised).
- `session.started` payload field `background_tasks`: evidenced values
  `"keep"` and `"kill"` (3 each in the local corpus). `"keep"` is **not**
  from the resume sample above (`01a0692b-…` used `"kill"`). **Unknown:**
  other enum values, selection rule, and headless semantics.
- `local-tracing/bootstrap/cli-*.log` — bootstrap traces, one file per CLI
  invocation; not the session transcript.
- `tool-outputs/.spool/` — truncated/in-flight tool bodies.
- `runtime/muse/ms-*.sock` — MSP session host sockets + `.lease` files.

Read-only; no files were modified except throwaway `/tmp/muse-2350-*` probes.

---

## 4. Hook / event inventory

### Durable `session.jsonl` payload types (corpus count, local store)

Command (snapshot at investigation time; later probes can increment):

```sh
grep -rho '"payload_type":"[^"]*"' ~/.local/share/muse/sessions | sort | uniq -c | sort -nr
```

Top types included `runtime.session`, `tool_batch.effect.*`,
`session.resource_pressure.observed`, `session.opened.observed`,
`session.end` (59), `session.resumed` (4), `session.started` (2),
`approval_wait.effect.started` (25), `approval_wait.effect.terminal` (18).
No `payload_type` containing `hook` was present in this corpus (no Muse
plugin installed: `muse plugins list --json` → `{"plugins":[]}`).

### Host hook event names (binary `HookEventKind`, 1.0.3-R2198.1)

Extracted from `muse-bin-1.0.3-R2198.1` strings (not live-dispatched):

`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`,
`PostToolUse`, `PreLLMCall`, `PostLLMCall`, `PreCompact`, `PostCompact`,
`SubagentStart`, `SubagentStop`, `Stop`, `SessionEnd`, `Notification`,
`PostToolUseFailure`, `StopFailure`, `PostToolBatch`.

Plugin packaging signals in the same binary:

- Manifest: root `plugin.json` with **Agent Plugins 1.0.0 `$schema`**, or
  exactly one nested `.muse-plugin` / `.codex-plugin` / `.claude-plugin`
  `plugin.json`.
- Conventional `hooks/hooks.json`, env `MUSE_PLUGIN_ROOT`.
- `muse plugins validate <path> --json` (empty probe failed
  `missing-manifest` with the message above).
- `muse plugins hook test <plugin-id>:<hook-id> --fixture <path>`.

**Unknown (explicit):**

- Exact `$schema` URL string (not recovered as a clean URL from the binary;
  `https://json.schemastore.org/agent-plugins-1.0.0.json` was **not**
  confirmed).
- Whether each `HookEventKind` is actually dispatched on `exec` vs TUI vs
  resume (no plugin installed; live hook probe is `v0.50.0-2`).
- Whether `SessionEnd` fires on `exec` process exit (durable log has
  `session.end`; that is **not** proof a plugin hook ran).
- Consolidation / Stop-exit-2 equivalent for Muse: **unknown**.

### Traceary coverage (this issue)

There is **no** `muse` host in `application/hostcoverage/matrix.json` and no
`integrations/muse-plugin/`. Traceary wiring is therefore **not present**.
The table below is an investigation forecast, not a matrix edit.

| Traceary lifecycle event | Muse host signal | Traceary status (today) | Owner |
| --- | --- | --- | --- |
| `session_started` | `SessionStart` (binary) | unsupported / unwired | v0.50.0-2 plugin + v0.50.0-3 matrix |
| `prompt` | `UserPromptSubmit` | unsupported / unwired | same |
| `command_executed` | `PostToolUse`, `PostToolUseFailure` | unsupported / unwired | same |
| `transcript` | `Stop` (turn boundary — **unverified** vs session end) | unsupported / unwired | same |
| `compact_summary` | `PreCompact`, `PostCompact` | unsupported / unwired | same |
| `session_ended` | `SessionEnd` in `HookEventKind`; durable `session.end` is a log event, not a hook | unsupported / unwired | live dispatch in -2 |
| `consolidation_request` | **unknown** | unsupported | -2 probe |

Extra host hooks with no Traceary lifecycle row yet: `PermissionRequest`,
`PreLLMCall`, `PostLLMCall`, `SubagentStart`, `SubagentStop`, `Notification`,
`PostToolBatch`, `StopFailure`. Classify after a live fixture.

Legend for a future matrix cell: **wired** = packaged Traceary capture;
**available** = host hook exists, Traceary does not subscribe; **unsupported**
= no usable host signal. Today every Muse×lifecycle cell is effectively
**unsupported** on the Traceary side because there is no package.

---

## 5. Sibling precedent (what a Muse plugin would need)

Do **not** build this here. Reference:

- Package: `integrations/grok-plugin/` — `plugin.json`, `hooks/hooks.json`,
  `scripts/traceary-grok.sh` (thin `traceary hook grok <action>`), `skills/`
  (four shared skills), `marketplace-entry.json`.
- Host matrix: `application/hostcoverage/matrix.go` + `matrix.json`.
  Status enum: `wired` / `available` / `unsupported`. Adding Muse means a new
  `hosts[]` entry (`id`, `package`, `doctor_client`, `events` for every
  `lifecycle_events` id), then `go run ./cmd/repo-tooling docs generate-host-coverage`.
- Doctor client, `traceary hook <client>`, install script, and bilingual
  `docs/integrations/` guide follow Grok/Kimi.

Muse-specific likely extras (from this investigation, for -2):

1. Manifest that `muse plugins validate` accepts (Agent Plugins 1.0.0
   `$schema` or `.muse-plugin/plugin.json`).
2. `hooks/hooks.json` using Muse `HookEventKind` names; commands via
   `MUSE_PLUGIN_ROOT`.
3. Headless orchestration: document `--disable-approval` / `--approval-mode never`
   / `--yolo`; detect stall via `session.jsonl` `approval_wait.effect.started`
   without terminal.
4. Do not assume `--json` stdout is a hook substitute.
5. Live-verify `exec` vs TUI vs `resume` dispatch before marking cells `wired`.

---

## Headless orchestration guidance (copy-paste)

Pinned: **Muse Code 1.0.3 (1.0.3-R2198.1)** /
`/Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1`.

```sh
muse exec --provider meta --trust-workspace \
  --approval-mode never \
  --max-model-steps N \
  --json \
  --prompt-file path.md
```

Stall: process alive, often **no child PIDs**,
`session.jsonl` has `approval_wait.effect.started` and `runtime.session`
`kind=approval` `requested`, and no matching `approval_wait.effect.terminal`.
Remedy: `--approval-mode never` or `--disable-approval` (or `--yolo` if
sandbox must also be off). Inspect:

```sh
rg 'approval_wait' ~/.local/share/muse/sessions/YYYY/MM/DD/<id>/session.jsonl
pgrep -P <muse-bin-pid> || echo 'zero child processes'
```
