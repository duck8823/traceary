# CLI reference

[日本語](./README.ja.md)

This page is the stable command reference for Traceary's public CLI surface.
Use it together with the quick-start section in `README.md`.

## Common conventions

- DB path resolution order: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- Mutating commands print human-friendly text by default
- Commands that create an event or session identifier support `--id-only` when scripts want the raw identifier
- Commands that support structured output expose `--json`
- JSON/NDJSON contract tests for CLI output are documented in [`../operations/json-contract-tests.md`](../operations/json-contract-tests.md).

## Core event commands

### `traceary log <message>`

Append a note event.

Defaults:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / detected workspace
- `--session-id`: flag → `TRACEARY_SESSION_ID` → latest non-stale active session for the resolved workspace → `default`

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`

Session resolution rules:

- explicit `--session-id` or `TRACEARY_SESSION_ID` wins
- otherwise Traceary reuses the latest non-stale active session for the resolved workspace
- when `remote.origin.url` is unavailable but the current directory is still inside a git worktree, Traceary falls back to the worktree root path as the work-context key
- if no workspace could be resolved or no matching active session is found, Traceary falls back to the historical `default` session ID

> **Note:** `log` and `audit` accept any `--session-id` value without validating whether the session actually exists. This is by design — hooks record events at high frequency and the extra DB lookup per write would add unacceptable overhead. If you pass a nonexistent session ID, the event is still recorded; it will simply not appear in session-scoped queries.

### `traceary audit <command> [<input>] [<output>]`

Record a command execution audit event.

Input styles:

- positional with command only: `traceary audit "go test ./..."`
- positional: `traceary audit "go test ./..." '{}' '{}'`
- named: `traceary audit --command "go test ./..." --input '{}' --output '{}'`

Useful flags:

- `--command`
- `--input`
- `--output`
- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

Command-audit input/output payloads are truncated before persistence when they exceed the resolved limit. The resolved limit is `--max-*-bytes` flag, then `TRACEARY_MAX_AUDIT_*_BYTES`, then `audit.max_*_bytes` in `~/.config/traceary/config.json`, then the built-in default. Truncated payloads preserve head and tail context and include structured `input_truncated` / `output_truncated` metadata plus an `original_bytes` marker; omitted bytes are not recoverable through `traceary show` or MCP `full_body=true`.

On **read surfaces** (`sessions --snapshot --json`, list-style recent-command panes), large host-tool payloads (`Edit` / `Write` / `Read` / shell) are projected into a tool-aware compact summary (tool name, path when present, rune counts, content hash, head/tail, and a `traceary show <event_id>` retrieval hint). This is a presentation-time projection only: raw persistence and `traceary show` remain full-fidelity.

Command strings also pass through the built-in best-effort secret redactors before storage. `input_redacted` / `output_redacted` only report input/output payload redaction; they do not expose a separate command-redaction flag.

Session resolution follows the same rules as `traceary log`.

## Read/query commands

### `traceary tui`

Open the Traceary operator cockpit TUI.

Use bare `traceary` in an interactive terminal when you want one terminal surface for the operator loop instead of remembering individual subcommands. `traceary tui` remains the explicit compatibility entrypoint for the same cockpit. The cockpit opens Tail-first and summarizes active work, recent failures, doctor status, and new events since the last live-tail visit. The Sessions tab stays session-centric (sessions, failures, commands, and health); memory candidates and stale-memory cleanup belong in the dedicated Memory tab. From the cockpit you can jump into live tail, doctor details, and memory inbox review.

`traceary tui` requires an interactive terminal. Non-TTY callers receive a refusal with exit code `2` and guidance to use the script-friendly commands instead (`list`, `sessions --snapshot [--json]`, `doctor --json`, `session handoff`, and `memory inbox list`; `top --snapshot [--json]` remains a permanent compatibility alias). Bare non-TTY `traceary` prints help plus fallback guidance rather than starting the cockpit.

Useful flags:

- `--db-path`
- `--reset-state` (reset local cockpit last-seen state before opening)

Compatibility:

- interactive bare `traceary` now opens the Tail-first cockpit by default
- use `traceary tui` when you prefer an explicit named entrypoint or need a stable compatibility path

### `traceary list`

List recent events.

`list` is the fast recent-history view. Use it when you already know the event kind / client / agent / session / workspace filters you want. Switch to `search` when you need keyword matching or date-range filtering.

Default text output is the same compact single-line shape as `tail` (`HH:MM:SS  kind  agent=<agent>  sess=<first-8>  ws=<basename>  message`, local time, no header). Pass `--wide` for the legacy tab-separated seven-column format, or `--utc` to force UTC timestamps. `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte. JSON without explicit `--fields` keeps the existing full event keys. Explicit JSON `--fields` emits only the selected keys; when `message` is absent, `list` and `search` use the body-free metadata query. Use `--fields ts,kind,message` to pick fields; precedence for text is `--fields` > preset fields > `read.fields` in `~/.config/traceary/config.json` > built-in default. `--fields` cannot be combined with `--wide`. Supported fields: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`, `source_hook`. Use `--preset <name>` to apply a saved view: built-in presets are `failures`, `prompts-only`, `compact-summaries`; user-defined entries in `read.presets` can override built-in names and explicit filter flags (`--kind`, `--failures`, `--workspace`, etc.) still win over a preset. Presets ignore `--wide` / `--json` for field overrides but still apply filters. Use `--color=auto|always|never` to toggle ANSI highlighting of compact rows (defaults to `auto`, honours the `NO_COLOR` env variable, and is ignored by `--wide` and `--json`). When highlighting is on, failed `command_executed` rows turn red+bold, `prompt` and `transcript` rows become cyan, `compact_summary` rows become magenta, and `session_started` / `session_ended` rows are dimmed.

Useful flags:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`

### `traceary tail`

Follow new events as they arrive.

`tail` is the live observation view. It prints a recent backlog first and then keeps following new matching events from the local store. Use it when you want to confirm that hooks are firing, that the expected session/workspace is receiving writes, or that failures are surfacing in real time. Unlike `list`, it does not exit after one snapshot. Unlike `search`, it does not perform keyword matching. Unlike `handoff`, it stays at the raw event-stream layer rather than assembling working memory.

Default text output is a compact single-line row (`HH:MM:SS  kind  agent=<agent>  sess=<first-8>  ws=<basename>  message`) that fits within ~100 columns and uses local time. Pass `--wide` for the legacy tab-separated seven-column shape, or `--utc` to force UTC timestamps in either text mode. `--wide --utc` reproduces the pre-v0.6.1 format byte-for-byte for scripts that parse it. `--json` emits newline-delimited JSON objects (one event per line) so pipelines can consume the stream incrementally; timestamps in JSON are UTC RFC3339Nano and are unaffected by `--utc`.

> The compact session ID (`sess=<first-8>`) is intended for human scanning only. For machine processing, use `--wide --utc` or `--json`.

Use `--fields ts,kind,message` to override the compact column order (precedence: flag > preset fields > `read.fields` in config.json > built-in default). `--fields` cannot be combined with `--wide`; see `traceary list` above for the full list of supported fields. Use `--preset <name>` for saved views (built-in: `failures` / `prompts-only` / `compact-summaries`; user-defined in `read.presets`). Use `--follow-session <prefix>` (minimum 8 runes) to scope the tail to one session — the value matches session ids by prefix so it is safe to paste from `traceary session list` output.

Useful flags:

- `--kind`
- `--limit`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--follow-session`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--failures`

### `traceary search [<query>]`

Search events by text and structured filters.

Text results use the same compact single-line format as `list` / `tail` (local time by default). Pass `--wide` for the legacy seven-column table, or `--utc` to force UTC timestamps. `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte. `--json` is unchanged. Use `--fields ts,kind,message` to override the compact column order (precedence: flag > preset fields > `read.fields` in config.json > built-in default); `--fields` cannot be combined with `--wide`, and the supported field list is shown under `traceary list` above. Use `--preset <name>` for saved views; a preset with filters can make a search with no free-text query valid because its filters count as constraints.

Useful flags:

- `--kind`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--since`
- `--until`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`

### `traceary timeline`

Show work timeline with gap-based block detection and per-workspace activity summaries.

`timeline` groups recent events into contiguous work blocks separated by idle gaps (default: 15 minutes) and prints one aligned sub-row per workspace inside each block. The per-workspace activity summary is picked using the fallback chain **`compact_summary` → first `prompt` → kind counts**, so whichever signal exists for that workspace in the block lights up the line. Default text output uses local time; pass `--utc` for UTC. `--json` emits UTC RFC3339Nano `start` / `end` timestamps, numeric `duration_sec`, and a `workspace_breakdown` array (`{workspace, event_count, kind_counts, agents, summary, summary_source}`).

Useful flags:

- `--workspace`
- `--from`
- `--to`
- `--gap` (idle gap threshold in minutes)
- `--limit`
- `--json`
- `--utc`

### `traceary replay`

Export a single-file HTML replay of recent sessions, events, and durable memories. The output is one self-contained `.html` — no external scripts, no fonts, no CDN — so it opens on an air-gapped laptop. Intended for incident reviews, weekly retrospectives, and sharing Traceary session history with teammates who don't run the CLI.

Useful flags:

- `--out` (required) — destination `.html` path
- `--sessions` (default 10) — number of recent sessions to include
- `--events-per-session` (default 20) — events per session
- `--memories` (default 20) — accepted memories to include
- `--timeline-blocks` (default 20) — timeline blocks rendered in the timeline panel; `<= 0` skips the panel
- `--hotspots` (default 10) — failure-hotspot clusters rendered in the hotspot panel; `<= 0` skips the panel

The replay HTML contains four panels (sessions, timeline blocks, failure hotspots, durable memories) plus a generated-at footer. The timeline and hotspot panels share the semantics of `traceary timeline` and `traceary list --failures-only` so operators can cross-reference either rendering.

Example: `traceary replay --out /tmp/replay.html`

### `traceary show <event-id>`

Show one event in detail.

Useful flags:

- `--json`

### `traceary context`

Print raw recent context events for another session or tool.

Useful flags:

- `--session-id`
- `--workspace`
- `--limit`
- `--json`

### `traceary session handoff`

Print a structured working-memory handoff summary built from session metadata, recent commands, compact summaries, and accepted durable memories. Pass `--compact-only` to emit the short prompt-injection summary form. `--compact-only` defaults `--recent` to 3 when the flag is not set.

The structured text keeps the legacy `RECENT_COMMANDS` string list and adds a sibling `RECENT_COMMAND_ITEMS` section. Each item identifies its event, reports returned/stored/original byte extent and response truncation, and includes the explicit `traceary show <event-id>` detail command. Handoff reads only a bounded body prefix; it does not hydrate full command payloads.

Useful flags:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`
- `--preset` (optional): apply a built-in retrieval preset (`resume` / `review` / `incident`) to durable memory filters
- `--as-of` (optional): evaluate durable memory validity at the given timestamp (YYYY-MM-DD or RFC3339); defaults to "now"
- `--compact-only` (optional): emit the short prompt-injection summary form; implicitly sets `--recent=3` unless `--recent` is given explicitly

> **v0.14 migration**: The former top-level `traceary handoff` and `traceary compact-summary` aliases were removed in v0.14.0. Running them now falls back to Cobra's generic unknown-command output — the targeted migration-error stubs were retired in v0.20.0. Use `traceary session handoff` (plus `--compact-only` for the compact form). See [CLI stability and deprecation policy](../cli-stability.md) for the full removal list.

## Durable memory commands

`traceary memory ...` is grouped by operator intent. The daily-read commands stay top-level; every other verb sits under one of three namespaces. The flat verbs from earlier releases (`memory remember`, `memory accept`, `memory hygiene scan`, ...) were removed in v0.15.0 after the v0.14 one-minor compatibility window; scripts and docs should use the canonical `memory inbox` / `memory store` / `memory admin` paths below. See the [memory command surface plan](../operations/memory-command-surface.md) for the historical mapping and the [CLI stability and deprecation policy](../cli-stability.md) for the deprecation contract.

```
memory
├── search           # daily read (top-level)
├── show             # daily read (top-level)
├── list             # daily read (top-level)
├── inbox            # candidate review surface
│   ├── list
│   ├── accept
│   ├── reject
│   └── review       # interactive TUI walk-through
├── store            # deliberate write/store workflows
│   ├── remember
│   ├── propose
│   └── distill
└── admin            # extraction + host-side I/O + maintenance + lifecycle
    ├── extract
    ├── import { codex | instructions }
    ├── export
    ├── activate
    ├── hygiene { scan | apply }
    ├── graph { add | list }
    ├── supersede
    ├── expire
    └── set-validity
```

### Daily-read commands

#### `traceary memory list`

List durable memories. When no explicit scope flag is set, `memory list` defaults to the resolved workspace scope.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory search [<query>]`

Search durable memories by text and structured filters. At least one query or filter is required.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory show <memory-id>`

Show one durable memory in detail, including evidence and artifact references.

Useful flags:

- `--json`

### `traceary memory inbox` — candidate review surface

Review the memory review queue. `list` surfaces `candidate` memories together with their confidence and review-readiness state plus evidence / artifact ref counts so a reviewer can judge provenance before accepting. `show` renders the evidence-first decision card for a single candidate. `accept` and `reject` take either a single positional id (the common interactive case) or `--ids id1,id2,...` for batch scripts and MCP callers; partial batches return a per-id success / failure breakdown so a failure never hides which entries transitioned. `--id-only` prints just the resulting memory id on stdout (mutually exclusive with `--json`); the canonical inbox surface is a strict superset of the v0.13.x positional-id form.

#### `traceary memory inbox list`

Useful flags: `--workspace`, `--agent`, `--session-family`, `--type`, `--source` (manual / extracted / extracted-hidden / imported / remember-intent / compact-summary), `--include-hidden`, `--limit`, `--offset`, `--json`.

The `--source` filter pairs naturally with the extraction and import paths:

- `--source imported` focuses on memories read from host-native sources such as Codex (see `memory admin import codex`).
- `--source extracted` focuses on memories `traceary memory admin extract` produced from session signals.
- `--source extracted-hidden` surfaces low-signal auto-extractions that are kept for audit but skipped from the default view.

#### `traceary memory inbox show <memory-id>`

Show one memory candidate as an evidence-first decision card. The text view includes the candidate fact, source context, confidence / review-readiness state, evidence refs, artifact refs, duplicate or supersede hints when available, and the accept-as-is checklist. Use this before accepting candidates whose `memory inbox list` `REVIEW` column says `needs-confirmation` or `blocked:no-evidence`.

Useful flags:

- positional `<memory-id>` for the candidate to inspect
- `--json`

#### `traceary memory inbox accept <memory-id>`

Accept one or more memory candidates.

Useful flags:

- positional `<memory-id>` for the common single-id case
- `--ids id1,id2,...` (repeatable) for batch scripts and MCP callers
- `--confidence`
- `--id-only` (mutually exclusive with `--json`)
- `--json`

#### `traceary memory inbox reject <memory-id>`

Reject one or more memory candidates.

Useful flags:

- positional `<memory-id>` for the common single-id case
- `--ids id1,id2,...` (repeatable) for batch scripts and MCP callers
- `--id-only` (mutually exclusive with `--json`)
- `--json`

#### `traceary memory inbox attach <memory-id>`

Attach evidence refs (and optional artifact refs) to an existing memory candidate without changing its review status. This is the script-friendly path for useful candidates that cannot be accepted or distilled yet because accepted memories require evidence. Artifact-only attachments are accepted only when the candidate already has evidence.

Useful flags:

- positional `<memory-id>` for the candidate to update
- `--evidence kind:value` (repeatable; at least one is required)
- `--artifact kind:value` (repeatable)
- `--id-only` (mutually exclusive with `--json`)
- `--json`

#### `traceary memory inbox review`

Interactive TTY walk-through over the memory review queue built on the shared Bubble Tea TUI foundation. Filters mirror `memory inbox list` so reviewers can pivot between the snapshot view and the interactive walk without re-tuning flags.

Action keys inside the screen:

- `a` accept the focused candidate
- `x` reject the focused candidate
- `s` skip (no state change, advance the cursor)
- `e` edit/distill — prompts for an operator-authored fact and routes through `traceary memory store distill --replace=supersede`. The original LLM-authored candidate text is never auto-accepted.
- `r` attach one or more evidence refs, plus optional `artifact:kind:value` refs, to the focused candidate; decisions apply in the order you queued them, so queue attach before accept/distill
- `v` view evidence / artifact refs for the focused candidate
- `?` toggle the help overlay
- `q` / Ctrl-C / Esc quit cleanly

The command refuses to start without a TTY and exits with code `2`, printing fallback guidance pointing at `memory inbox list` plus `memory inbox accept|reject` for batch / scripted callers (so non-interactive shells branch deterministically). Accept, reject, and evidence attach reuse the same memory usecase as the batch commands; dedupe / status transitions are unchanged. If a queued per-id decision fails after the TUI exits, the summary still prints each `FAILED` row on stdout and the command returns a non-zero error so shell callers do not treat a partial review as successful.

Useful flags: `--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`.

#### `traceary memory inbox cleanup`

Bulk preview or reject stale / low-quality memory candidates. Dry-run by default; pass `--apply` to reject the matched candidates. Filters: `--quality {low|normal|any}` (default `low`; `--quality any` requires `--older-than` so the whole queue is not rejected), `--source`, `--type`, `--workspace`, `--agent`, `--session-family`, `--older-than` / `--newer-than`, `--include-hidden`, `--limit`. The text and `--json` output include a composition `summary` — `total` plus counts `by_source` and `by_type` — so the batch makeup is visible before `--apply`. Cleanup only rejects candidates; accepting stays on the per-candidate review surfaces above to keep the evidence-first rails intact.

### `traceary memory store` — deliberate writes

Every command under `memory store` writes a durable-memory row, regardless of whether the row lands as `accepted` or `candidate`.

#### `traceary memory store remember`

Record an accepted durable memory directly.

Useful flags:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--id-only`
- `--json`

#### `traceary memory store propose`

Record a memory candidate that still needs review.

Useful flags are the same as `memory store remember`, except `--confidence` is ignored.

#### `traceary memory store distill`

Create a new accepted durable memory from one or more existing memory candidate IDs using an operator-provided fact. Evidence refs and artifact refs from the source memory candidates are unioned onto the accepted memory; Traceary does not rewrite or accept content automatically.

Useful flags:

- `--from` — comma-separated source memory candidate IDs (repeatable)
- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family` (one is required)
- `--confidence`
- `--source`
- `--replace=keep|reject|supersede`
- `--id-only`
- `--json`

### `traceary memory admin` — extraction, host I/O, maintenance, lifecycle

Operator-facing concerns: extraction (mines candidates from existing sessions), host-native I/O (`import` / `export` / `activate`), maintenance (`hygiene` / `graph`), and the lifecycle verbs that mutate already-stored rows (`supersede` / `expire` / `set-validity`).

#### `traceary memory admin extract`

Extract memory candidates from a target session using session summaries, compact summaries, prompt events, and note/review signals. Extraction is candidate-only: Traceary never auto-accepts the extracted memories. Prompt events are optional; missing prompt or compact-summary events simply reduce the available signals. When `--session-id` is omitted, Traceary resolves the active session first and then falls back to the latest matching session in the workspace. `Feedback:` / `Correction:` labels are preserved by mapping them into the current minimal durable-memory taxonomy as `preference` candidates. Persisted candidates go through the same sanitization/redaction path as other durable-memory writes before they are stored.

Useful flags:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--debug-signals`
- `--json`

#### `traceary memory admin import codex`

Import memory candidates from a local Codex memory layout — by default every `*.md` file under `~/.codex/memories`. Legacy `MEMORY.md` keeps the handbook allow-list (`## User preferences`, `## Reusable knowledge`, `## Failures and how to do differently`), while additional Markdown shards import bullet/list items under any heading. Each candidate gets `source=imported`, file evidence/artifact refs with line ranges, and a scope resolved from the Codex `applies_to: cwd=...` hint (falling back to `--workspace` when the source file does not declare one). Sanitization runs on every imported fact, and nothing is auto-accepted. Re-running the command is idempotent: existing candidates (at any lifecycle status, including rejected) suppress a duplicate import so previously reviewed memories are never resurrected.

Useful flags:

- `--root` — Codex memory root (defaults to `~/.codex/memories`)
- `--workspace` — fallback scope when the source file has no `applies_to` hint
- `--watch` — keep polling instead of exiting after one run
- `--interval` — polling interval when `--watch` is set (minimum 1s)
- `--json`

#### `traceary memory admin import instructions`

Read a host instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) and create `candidate` durable memories for every bullet outside the Traceary-managed block. Bullets inside the managed block are already represented in the store via export, so they are intentionally skipped.

Useful flags:

- `--source` — host that wrote the file (`claude` / `codex` / `gemini`)
- `--in` — path to the instruction file
- `--workspace` — scope assigned to imported candidates (defaults to env/detected workspace)
- `--json` — print JSON output

#### `traceary memory admin export`

Write the accepted durable memories for the current scope into a host-native instruction file (CLAUDE.md, AGENTS.md, or GEMINI.md). Output is deterministic and idempotent: re-running with unchanged memories produces a byte-identical file, and every Traceary-managed block is bracketed by `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` markers so a later `memory admin import instructions` run can round-trip without creating duplicates.

Useful flags:

- `--target` — one of `claude`, `codex`, `gemini`
- `--workspace` — workspace scope filter (defaults to env/detected workspace). Workspace exports include `global` memories by default so host-level rules follow the repository export.
- `--include-global` — include `global` memories alongside the workspace scope (default `true`)
- `--no-global` — opt out and export only the explicit workspace scope
- `--out` — output path; pass `-` (or omit) to write to stdout
- `--json` — print a summary of the export result in addition to writing the file

#### `traceary memory admin activate`

Activate accepted memories into a host's native context surface using safe,
explicit writes. The same flag set works for **Codex**, **Claude**, and
**Gemini**; the resolved target paths and managed-region layout differ per
host (see the [host-native memory activation ADR](../architecture/host-native-memory-activation.md)
and the [durable memory guide](../memory/README.md#activation-strategy-by-host)).

Modes are mutually exclusive — exactly one of `--status`, `--dry-run`, or
`--apply` must be passed. `--diff` is only valid with `--dry-run`.

| Mode | Effect |
| --- | --- |
| `--status` | Read-only. Reports `missing` / `stale` / `in_sync` / `invalid` plus a per-component breakdown for two-file targets. Emits `next_dry_run` and `next_apply` remediation commands when refresh is needed. |
| `--dry-run [--diff]` | Read-only. Prints the planned content; with `--diff` prints a diff against the existing target file(s). For two-file targets the output is labeled `external memory plan` / `host context plan` (or the corresponding diffs). |
| `--apply` | Mutating. Writes the target safely (lstat → reject symlinks/directories → atomic temp-file rename, parent directory creation only for the file being written). Idempotent; re-running converges to noop. Refuses newer marker versions. |

Default targets:

- `codex` — Traceary-managed file at `~/.codex/memories/traceary.md`. Single-file target; the whole file is owned by Traceary.
- `claude` — host context `<root>/CLAUDE.md` plus external file `<root>/.traceary/memories/claude.md`. Activation root defaults to the nearest `.git` ancestor or cwd.
- `gemini` — host context `<root>/GEMINI.md` plus external file `<root>/.traceary/memories/gemini.md`. Same root resolution as Claude. Gemini's `## Gemini Added Memories` section is preserved byte-for-byte; the managed import stub is appended after it.

Useful flags:

- `--target` — `codex`, `claude`, or `gemini` (required)
- `--dry-run` — print the activation plan without writing or creating files
- `--apply` — write the activation target file (and external memory file for two-file targets)
- `--status` — read-only comparison with the current accepted memories
- `--root` — activation root override (Codex: memory root; Claude/Gemini: project root containing the host context file)
- `--path` — explicit activation target file override; for Claude/Gemini this points at the host context file, and the external memory file is derived as `<dir-of-path>/.traceary/memories/<target>.md`
- `--workspace` / `--include-global` / `--no-global` — activation scope controls
- `--diff` — include a diff when the target file exists (dry-run only)
- `--json` — print the activation plan, status, or apply result as JSON. For two-file targets the JSON exposes `host_context` and `external_memory` components with per-file `path`, `state`, `action`, `existing`, and (for plans) `markdown` / `diff`.

##### Examples

Status (read-only, run inside a project for Claude/Gemini so the activation
root resolves to the nearest ancestor that contains `.git`):

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target claude --status --json
traceary memory admin activate --target gemini --status
```

Dry-run with diff against existing files:

```sh
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target claude --dry-run --diff
traceary memory admin activate --target gemini --dry-run --diff
```

Apply (idempotent — safe to re-run):

```sh
traceary memory admin activate --target codex --apply
traceary memory admin activate --target claude --apply
traceary memory admin activate --target gemini --apply
```

Override the activation root or the host context file path explicitly:

```sh
# scope Claude activation to a specific project directory regardless of cwd
traceary memory admin activate --target claude --root /path/to/project --status

# point Gemini at an explicit host context file (external file is derived)
traceary memory admin activate --target gemini --path /path/to/GEMINI.md --apply
```

##### Recovering from `invalid`

If `--status` reports `invalid`, do not blindly re-run `--apply` — the apply
path will refuse the same target. Inspect the per-component state with
`--json`, fix the root cause, then re-run `--status`:

| Cause | Recovery |
| --- | --- |
| Symlink or directory at the target | Replace with a regular file or remove it |
| Duplicate / orphan / malformed managed markers | Restore the original markers, or remove the managed region entirely |
| Newer marker version present | Upgrade the local Traceary binary, or remove the managed block and re-apply |
| Unmanaged import line outside any Traceary stub already points at the expected `.traceary/memories/<host>.md` | Remove the unmanaged line, then re-run apply |
| Host context file is `invalid` but external memory looks fine (or vice versa) | Use the JSON `host_context.state` / `external_memory.state` fields to identify the offending file before editing |

#### `traceary memory admin hygiene scan`

Scan `accepted` durable memories and surface hygiene suggestions without mutating the store:

- `redaction_hit` — the current audit redaction rules mask content the stored fact still exposes (for example after the operator added a new extra pattern to `~/.config/traceary/config.json`). The suggestion carries `sanitized_fact` so a follow-up `memory admin supersede` call has a ready replacement.
- `expiry_candidate` — the memory has not been updated for longer than `--expiry-days`; the operator may want to expire it.
- `duplicate` — two or more `accepted` memories share the same scope + fact pair; one should supersede or expire the other.
- `supersede_candidate` — two accepted memories in the same scope differ in text but share a word-Jaccard similarity at or above `--similarity` (default 0.6). The older memory is the supersede target; the newer memory's fact is the suggested replacement (`replacement_memory_id`, `replacement_fact`, `similarity`).
- `validity_overlap_supersede` — `(scope, type)`-sharing pairs whose explicit `[valid_from, valid_to)` windows overlap; reported in preference to `supersede_candidate` when both apply.

Useful flags:

- `--workspace` — scope filter (defaults to env/detected workspace; leave empty to scan every scope)
- `--expiry-days` — staleness threshold in days (default 90)
- `--similarity` — word-Jaccard threshold for supersede_candidate detection, between 0.0 and 1.0 (0 uses the built-in default 0.6)
- `--json` — print JSON output with per-suggestion metadata

#### `traceary memory admin hygiene apply`

Commit the lifecycle transition implied by each suggestion for the memories in `--ids`. The usecase re-runs the scan first so stale ids (memories the operator already resolved) land in the failure list instead of silently mutating state. Transitions applied:

- `redaction_hit` → `supersede` with the sanitized fact, inheriting the existing scope / type / refs.
- `expiry_candidate` → `expire` at the current time.
- `duplicate` → `reject` the duplicate copy (pick the id whose partner you want to keep).
- `supersede_candidate` / `validity_overlap_supersede` → `supersede` with the newer memory's fact (scope / type / refs inherited from the older memory).

Useful flags:

- `--ids` — comma-separated memory ids (repeatable)
- `--expiry-days` — staleness threshold used by the internal scan (default 90)
- `--json` — print JSON output with per-id transition metadata

#### `traceary memory admin graph add <from-memory-id> --to <to-memory-id> --relation <type>`

Record a typed relationship between two memories (graph overlay introduced in v0.9.0). See [temporal memory architecture](../architecture/temporal-memory.md) for the relation vocabulary and overlay design.

Useful flags:

- `--to`: target memory ID (required)
- `--relation`: `supersedes` / `contradicts` / `supports` / `related-to` / `causes` (required; unknown values are accepted for forward compatibility)
- `--from`: validity window lower bound (YYYY-MM-DD or RFC3339); defaults to "now"
- `--to-date`: validity window upper bound (exclusive); open-ended when omitted
- `--json`

#### `traceary memory admin graph list`

List edges matching the given filters. Uses the same half-open `[valid_from, valid_to)` semantics as `memory list --as-of`.

Useful flags:

- `--memory-id`: restrict to edges touching this memory (source or target)
- `--relation`: filter by relation type
- `--as-of`: evaluate validity at a given timestamp
- `--limit`
- `--json`

#### `traceary memory admin supersede <memory-id>`

Replace an accepted durable memory with a new accepted memory. Omitted `--type` and scope flags inherit from the current memory.

Useful flags:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--from` / `--to` — content validity window for the replacement
- `--id-only`
- `--json`

#### `traceary memory admin expire <memory-id>`

Expire an active durable memory.

Useful flags:

- `--at`
- `--id-only`
- `--json`

#### `traceary memory admin set-validity <memory-id>`

Set or update the content validity window (`valid_from` / `valid_to`) on a durable memory. The validity window describes when the fact is asserted to be true and is independent of the lifecycle `expires_at` written by `memory admin expire`.

Useful flags:

- `--from` — start of the window (`YYYY-MM-DD` or RFC3339)
- `--to` — end of the window
- `--clear-to` — return to open-ended validity (mutually exclusive with `--to`)
- `--id-only`
- `--json`

### Removed flat aliases (v0.15)

The flat memory verbs from earlier releases were hidden deprecated aliases during v0.14.x and were removed in v0.15.0. They are kept here only as a historical migration note; do not use them in new scripts or docs.

| Removed alias (v0.15) | Canonical replacement |
| --- | --- |
| `memory accept <id>` | `memory inbox accept <id>` |
| `memory reject <id>` | `memory inbox reject <id>` |
| `memory remember` | `memory store remember` |
| `memory propose` | `memory store propose` |
| `memory distill` | `memory store distill` |
| `memory extract` | `memory admin extract` |
| `memory import codex` | `memory admin import codex` |
| `memory import instructions` | `memory admin import instructions` |
| `memory export` | `memory admin export` |
| `memory activate` | `memory admin activate` |
| `memory hygiene scan` | `memory admin hygiene scan` |
| `memory hygiene apply` | `memory admin hygiene apply` |
| `memory graph add` | `memory admin graph add` |
| `memory graph list` | `memory admin graph list` |
| `memory supersede` | `memory admin supersede` |
| `memory expire` | `memory admin expire` |
| `memory set-validity` | `memory admin set-validity` |

## Session commands

### `traceary session start`

Record a session start boundary and print the session ID.

Defaults:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / detected workspace
- `--session-id`: generate a new ID when omitted

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--parent-session-id`
- `--id-only`
- `--json`

### `traceary session end`

Record a session end boundary and print the resulting event ID.

Defaults:

- `--session-id`: flag → `TRACEARY_SESSION_ID`
- missing `--client` / `--agent` / `--workspace` values are backfilled from the matching `session start` when available

Useful flags:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--summary`
- `--id-only`
- `--json`

<a id="traceary-top"></a>

### `traceary sessions`

Launch a live multi-pane dashboard that splits the workspace view into five panes: active sessions (root → child), recent failures, recent `command_executed` events, memory review queue candidates, and stale durable memories that may need cleanup. Each pane scrolls independently; `tab` / `shift+tab` cycle pane focus, `↑/↓` (or `k/j`) scroll by one row, `pgup/pgdn` page, `g/G` jump to top/bottom, `r` forces a refresh, `?` toggles a help overlay, and `q` / Ctrl-C quit cleanly. `/` opens an incremental search prompt for the focused pane; Enter keeps the current search filter, and Esc clears the active filter without quitting. Enter on a highlighted row opens a detail modal for the selected session, event, or memory; Esc / `q` close the modal and Ctrl-C still quits the dashboard. Idle sessions are dimmed in the sessions pane when their latest activity is older than `--idle`; they are not hidden. Non-TTY callers (pipes, CI logs) automatically fall back to the snapshot text writer. `traceary top` remains available as a permanent compatibility alias, and `traceary session tree` remains the static retrospective view.

Snapshot output mirrors the dashboard panes so non-interactive consumers (pipes, CI, scripts) see the same data the live view shows. The text snapshot starts with a `RELIABILITY` section, then prints `ACTIVE SESSIONS`, `RECENT FAILURES`, `RECENT COMMANDS`, `CANDIDATE MEMORIES (count=N remember_intent=M)`, and `STALE MEMORIES (count=N)` sections; empty panes print a stable empty-state line so headers always render. The JSON snapshot is wrapped in an envelope with `sessions`, `failures`, `recent_commands`, `candidates` (`{ count, remember_intent_count, items }`), `stale_memories` (`{ count, items }`), and `reliability` keys; each session node keeps the same fields earlier releases emitted. `reliability.memory` additionally carries a `candidate_hygiene` object (`stale_count`, `duplicate_count`, `fragment_like_count`, `extracted_hidden_count`, `likely_actionable_count`) summarising the hygiene composition of the scanned candidate window: the four flag counts are independent diagnostic dimensions and may overlap, `likely_actionable_count` is the complement (candidates flagged by none), and — like `accepted_count` / `candidate_count` — they reflect the scanned sample when `scan_limit_reached` is true. `duplicate_count` counts exact duplicates only (same scope, memory type, and fact, matching the extraction dedupe key); similarity duplicates stay in `traceary memory admin hygiene scan`. Per-pane row caps follow the dashboard defaults (50 failures, 50 recent commands, 25 candidates, 25 stale memories); the session pane keeps using `--limit`.

### Operator vs AI-safe JSON profiles

`--snapshot --json` defaults to the full **operator** envelope (dashboard panes with truncated bodies). For agent resume / handoff, prefer:

```sh
traceary sessions --snapshot --json --profile ai
```

The `ai` profile keeps a bounded envelope:

- `profile: "ai"` in the JSON payload
- session identity + `latest_event_id` with retrieval hints (`traceary show <event_id>`) instead of large latest-event bodies
- small failure / recent-command samples as ID + kind + retrieval hints (no raw bodies)
- memory candidate / stale counts and reliability hygiene without candidate fact arrays
- tighter session / pane caps (does not change the default operator profile)

Use the operator snapshot for humans, dashboards, and full-fidelity scripts. Use `--profile ai` when an agent should decide next steps without re-amplifying large command bodies or inbox facts.

Example snapshot:

```sh
traceary sessions --snapshot
```

```text
RELIABILITY:
- stale_active_sessions=0 hint="ok"
- memory_counts accepted=3 candidate=1 accepted_ratio=75% hint="review memory candidates with `traceary memory inbox review` and cleanup old candidates with `traceary memory inbox cleanup --dry-run`"
- candidate_age count=1 oldest=2026-04-10T12:00:00Z newest=2026-04-10T12:00:00Z avg_age=6h0m hint="prioritize older memory candidates first"
- large_payloads count=0 recent_commands=0 recent_failures=0 sampled=2 body_limit=500 hint="inspect full payloads with `traceary show <event_id>`; keep command output concise for handoff/top surfaces"

ACTIVE SESSIONS:
4a70c526 name="github.com/duck8823/traceary · codex" workspace=github.com/duck8823/traceary agent=codex client=claude started=07:06:37 latest=07:06:58 events=165 last=transcript: investigating failing tests
└── 7c91a2bf name="github.com/duck8823/traceary · worker" workspace=github.com/duck8823/traceary agent=worker client=claude started=07:03:12 latest=07:06:52 events=42 last=command_executed: go test ./presentation/cli

RECENT FAILURES:
07:06:58 command_executed go test ./presentation/cli [exit=1]

RECENT COMMANDS:
07:06:52 command_executed go build ./...

CANDIDATE MEMORIES (count=1 remember_intent=1):
mem-1 preference prefer table-driven subtests

STALE MEMORIES (count=1):
mem-stale-1 decision workspace:duck8823/traceary superseded superseded rollout note
```

Active session columns:

- `name` — operator display name inserted before raw metadata; uses `label` first, then `summary`, then `workspace · agent`, then workspace, agent, or the short session id; quoted with Go-style `%q` escaping and capped by the same message truncation rule
- `workspace` — compact workspace path; tail is preserved when truncated so the repo qualifier stays readable
- `agent` — most specific agent / subagent role
- `client` — recording client
- `started` — session start time
- `latest` — latest event time
- `events` — total event count
- `last` — latest event as `<kind>: <message>`, or `-` when there is no event yet (the message is scrubbed of newlines / control characters and capped at 80 runes)
- `idle` — appended when latest activity is older than `--idle`

Useful flags:

- `--workspace`
- `--client`
- `--agent`
- `--idle <duration>` — dim rows older than the threshold without hiding them
- `--snapshot --json` — print a one-shot JSON envelope with `sessions`, `failures`, `recent_commands`, `candidates` (`{ count, remember_intent_count, items }`), `stale_memories` (`{ count, items }`), and `reliability`. Each session node carries `latest_event_kind`, `latest_event_message`, `latest_event_id`, and `latest_event_at` in addition to the standard session fields; `latest_event_message` is truncated to the shared 500-rune body cap so a noisy command/tool payload is not re-amplified into the next agent's context, and when it is cut the node adds `latest_event_message_truncated`, `latest_event_message_length`, and `latest_event_message_bytes` — fetch the full body explicitly with `traceary show <latest_event_id>`. `reliability.large_payloads` additionally carries a bounded `samples` array; each sample is body-safe metadata (`event_id`, `kind`, `source`, `message_length`, `message_bytes`, `first_line`, `retrieval_hint`) and never the full body. Failures and recent commands reuse the standard event JSON shape (also body-capped); memory candidates reuse the durable-memory summary JSON shape; stale memories reuse durable-memory summary fields plus a `reason`. `traceary session tree --json` keeps its independent contract and does not expose any of these surfaces
- `--limit`

### `traceary session list`

List session summaries.

The session list view surfaces session metadata such as `label`, `summary`, and `parent_session_id` together with status, duration, and aggregate counts.

Useful flags:

- `--workspace`
- `--agent`
- `--label`
- `--from`
- `--to`
- `--limit`
- `--offset`
- `--json`

### `traceary session tree`

Render the parent → child → grandchild lineage for every loaded session. Each row shows the session id, status, most specific subagent role (for example `claude/Explore` for Claude Code subagents), workspace, duration, and an `N cmds/M events` breakdown. Children of the same parent are ordered by `spawn_order` ascending; top-level sessions with no `spawn_order` are ordered by `started_at`. The JSON surface adds `parent_session_id`, `spawn_event_id`, `subagent_kind`, `spawn_order`, `depth`, numeric `duration_sec`, and `subagent_type` to every node so external tooling can reason about lineage without replaying the text format.

Useful flags:

- `--workspace`
- `--limit`
- `--root <session-id>` — focus on the subtree rooted at the given session
- `--ongoing-only` — keep only lineages that still contain an active session
- `--json`

### `traceary session lineage <session-id>`

Render the full lineage that contains a session: Traceary walks up from `<session-id>` to the topmost ancestor, then returns that root and all descendants using the same text and JSON node shape as `session tree`.

Useful flags:

- `--json`

### `traceary session label <label-text>`

Set or update a session label.

Defaults:

- `--session-id`: flag → `TRACEARY_SESSION_ID`

Useful flags:

- `--session-id`
- `--db-path`

### `traceary session latest`

Print the latest session ID matching the current filters.

`latest` means the session whose most recent lifecycle boundary (`session start` or `session end`) is newest among the matches.

Useful flags:

- `--client`
- `--agent`
- `--workspace`
- `--json`

### `traceary session active`

Print the active session ID matching the current filters.

Useful flags:

- `--client`
- `--agent`
- `--workspace`
- `--stale-after`
- `--allow-stale`
- `--json`

### Session status values

`session list`, `session tree`, and the `sessions --snapshot` / `top --snapshot` JSON `status` field report one of:

| Status | Meaning |
|--------|---------|
| `active` | No end marker and within the stale window. |
| `stale` | No end marker but started before the stale window (default 24h). |
| `ended` | Has an end marker and no events after it. |
| `ended_with_late_events` | Has an end marker but later events arrived under the same session. The end marker can come from a `session_ended` event or from `session gc` writing `ended_at` directly. |

The active-only snapshot keeps `active`, `ended_with_late_events`, and (with `--allow-stale`) `stale` sessions. `ended_with_late_events` is what stops `sessions --snapshot` from returning zero sessions when recent workspace events exist even though the session was already closed — for example when a host such as Codex closed the session early but the conversation kept going. CLI snapshot and MCP `session_status(action="active")` apply the same rule, so a session with events after its end marker is surfaced by both.

## Hooks and diagnostics

### `traceary completion <bash|zsh|fish|powershell>`

Generate shell completion scripts for interactive use.

### `traceary hooks print`

Print generated hook configuration for a supported client.

Supported clients: `claude`, `codex`, `gemini`
Aliases: `claude-code`, `codex-cli`, `gemini-cli`

Useful flags:

- `--client`
- `--traceary-bin`

### `traceary hooks install`

Install generated hook configuration into the standard client config path.

Useful flags:

- `--client`
- `--project-dir`
- `--traceary-bin`
- `--output`
- `--global` (write to the user-level config; mutually exclusive with `--output`)
- `--force`

### `traceary hooks guide`

Print the recommended install/check/verify steps for a supported client.

Useful flags:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

Diagnose DB access, generated hook configuration presence, MCP registration, plugin version alignment, and client config integration.

Text output is grouped into stable sections: `Environment`, `Database`, `Plugins`, `MCP`, and `Hooks`.
Each check has a severity: `PASS`, `WARN`, or `FAIL`. `WARN` means Traceary found a first-run / not-configured-yet state, such as a missing host config file before hooks are installed, more than one `traceary` executable on `PATH`, an MCP registration that points at a stale binary, or an installed plugin version that does not match the running `traceary` binary. `FAIL` means Traceary found a broken runtime state, such as DB access problems, unreadable / invalid config, or `traceary` not being available on `PATH`.

Additional doctor checks:

- `path` confirms `traceary` resolves on `PATH` and reports the directory. Missing is `FAIL`; multiple matches are `WARN`.
- `<client>-mcp` checks Claude Code, Codex, and Gemini config/plugin registration for the `traceary mcp-server` MCP server.
- `<client>-plugin-version` compares detected installed plugin manifests/caches with the running binary version and suggests reinstalling/updating the plugin when they drift.
- `claude-hook-cancellations` separates actionable SessionEnd cancellation markers from markers whose referenced session has subsequently ended. `doctor --fix --dry-run` previews removal of resolved markers; `doctor --fix` removes only those proven resolved and leaves active, missing-session, or unreadable evidence untouched.
- `codex-memory-activation` / `claude-memory-activation` / `gemini-memory-activation` check whether accepted durable memories are missing, stale, in sync, or invalid in the host's native activation target. `missing` and `stale` are reported as `WARN` with exact `memory admin activate --dry-run --diff` (preview) and `memory admin activate --apply` (refresh) remediation commands; `invalid` is reported as `FAIL` with a hint to inspect the host file before applying. Run with `--client <claude|codex|gemini>` to scope the report and with `--project-dir <dir>` to pin the Claude/Gemini activation root to a specific repository instead of the doctor process's working directory.

Exit codes:

- `0`: all checks are `PASS`
- `1`: at least one check is `FAIL`
- `2`: at least one check is `WARN` and no checks are `FAIL`

The default warning-only exit code stays non-zero so interactive and strict
automation notice drift. CI and smoke checks that only need to fail on broken
states should use `traceary doctor --json --warnings-ok`; warning counts and
per-check `WARN` statuses remain available in the JSON report, and failures
still exit `1`.

`--json` keeps the legacy top-level `checks` list and adds a sectioned structure:

```json
{
  "sections": [
    {
      "name": "Environment",
      "checks": [
        {"name": "config", "severity": "PASS", "section": "Environment", "message": "...", "hint": "", "fix_command": ""}
      ]
    }
  ],
  "summary": {"pass": 3, "warn": 1, "fail": 0},
  "exit_code": 2
}
```

Alias:

- `traceary status`

Useful flags:

- `--client`
- `--project-dir`
- `--json`
- `--fix` — apply available safe remediations
- `--dry-run` — preview `--fix` without writing
- `--warnings-ok` — return exit code `0` for warning-only reports while keeping failures at exit code `1`
- `--strict` — audit-reliability: report every exact duplicate group regardless of time, not only near-simultaneous writes

## Store administration (`traceary store ...`)

Store administration commands live under the `store` namespace. The old top-level `traceary init`, `traceary backup`, and `traceary gc` aliases were removed in v0.14.0; running them now returns Cobra's unknown-command error (use `traceary store init`, `traceary store backup ...`, `traceary store gc`). The aliases shipped a deprecation notice from v0.9.0 through v0.13.x. See [CLI stability and deprecation policy](../cli-stability.md).

### `traceary store init`

Explicitly create the DB and apply migrations up front. Optional because normal commands initialize the store on demand.

### `traceary store backup create`

Create a compact SQLite backup file.

Useful flags:

- `--output`
- `--db-path`
- `--force`

### `traceary store backup restore`

Restore the DB from a backup file.

Useful flags:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary store gc`

Delete retained store records and compact the SQLite store. By default, `--target all` applies retention to events, empty ended sessions, expired/superseded memories, and closed memory edges. Use `--target events` to keep the legacy event-only behavior.

Useful flags:

- `--keep-days`
- `--target events|sessions|memories|memory_edges|all`
- `--dry-run`

## Integration commands

> The entire `integration` command subtree (the `integration` parent and the `codex` group) is hidden from `traceary --help` as of v0.20.0 and is scheduled for full removal in v0.21.0. The entries below are kept only as migration notes; the hidden stubs still exit non-zero with a pointer to Codex's official `/plugins` flow.

### `traceary integration codex install` (retired, hidden)

Retired in v0.14.0 and **not a supported install surface**. The command is hidden and no longer installs anything; invoking it only prints a hint pointing to Codex's official `/plugins` flow. New installs must go through Codex's official `/plugins` flow (run `codex` inside the repository → `/plugins` → `Traceary Plugins` → `Traceary`). See the [Codex plugin guide](../integrations/codex-plugin.md) for the full setup.

### `traceary integration codex uninstall` (removed in v0.15)

Removed in v0.15.0 and **not a supported uninstall surface**. The name is kept here only as a historical migration note: use Codex's official `/plugins` flow to uninstall the Traceary plugin, and use the [manual cleanup steps in the Codex plugin guide](../integrations/codex-plugin.md) only for state left behind by the retired pre-v0.14 install path.

### `traceary mcp-server`

Run the MCP server over stdio for AI client integration.

## Related docs

- onboarding and quick start: [`../../README.md`](../../README.md)
- environment variables and runtime assumptions: [`../environment/README.md`](../environment/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
