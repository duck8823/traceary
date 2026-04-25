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

Session resolution follows the same rules as `traceary log`.

## Read/query commands

### `traceary list`

List recent events.

`list` is the fast recent-history view. Use it when you already know the event kind / client / agent / session / workspace filters you want. Switch to `search` when you need keyword matching or date-range filtering.

Default text output is the same compact single-line shape as `tail` (`HH:MM:SS  kind  agent=<agent>  sess=<first-8>  ws=<basename>  message`, local time, no header). Pass `--wide` for the legacy tab-separated seven-column format, or `--utc` to force UTC timestamps. `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte. `--json` is unchanged. Use `--fields ts,kind,message` to pick which compact columns are shown; precedence is `--fields` > preset fields > `read.fields` in `~/.config/traceary/config.json` > built-in default. `--fields` cannot be combined with `--wide`. Supported fields: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`. Use `--preset <name>` to apply a saved view: built-in presets are `failures`, `prompts-only`, `compact-summaries`; user-defined entries in `read.presets` can override built-in names and explicit filter flags (`--kind`, `--failures`, `--workspace`, etc.) still win over a preset. Presets ignore `--wide` / `--json` for field overrides but still apply filters. Use `--color=auto|always|never` to toggle ANSI highlighting of compact rows (defaults to `auto`, honours the `NO_COLOR` env variable, and is ignored by `--wide` and `--json`). When highlighting is on, failed `command_executed` rows turn red+bold, `prompt` and `transcript` rows become cyan, `compact_summary` rows become magenta, and `session_started` / `session_ended` rows are dimmed.

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

Print a structured working-memory handoff summary built from session metadata, recent commands, compact summaries, and accepted durable memories. Pass `--compact-only` to emit the short prompt-injection summary (replaces `traceary compact-summary`). `--compact-only` defaults `--recent` to 3 (matching the legacy behavior) when the flag is not set.

Useful flags:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`
- `--preset` (optional): apply a built-in retrieval preset (`resume` / `review` / `incident`) to durable memory filters
- `--as-of` (optional): evaluate durable memory validity at the given timestamp (YYYY-MM-DD or RFC3339); defaults to "now"
- `--compact-only` (optional): emit the short prompt-injection summary form (replaces `traceary compact-summary`); implicitly sets `--recent=3` unless `--recent` is given explicitly

> **v0.8 → v0.9 migration**: The former top-level `traceary handoff` and `traceary compact-summary` commands are deprecated aliases that print a notice and will be removed in v1.0. Use `traceary session handoff` (plus `--compact-only` for the compact form).

## Durable memory commands

### `traceary memory list`

List durable memories. When no explicit scope flag is set, `memory list` defaults to the resolved workspace scope.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory search [<query>]`

Search durable memories by text and structured filters. At least one query or filter is required.

Useful flags:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory show <memory-id>`

Show one durable memory in detail, including evidence and artifact references.

Useful flags:

- `--json`

### `traceary memory remember`

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

### `traceary memory propose`

Record a candidate durable memory that still needs review.

Useful flags are the same as `memory remember`, except `--confidence` is ignored.

### `traceary memory extract`

Extract candidate durable memories from a target session using session summaries, compact summaries, prompt events, and note/review signals. Extraction is candidate-only: Traceary never auto-accepts the extracted memories. Prompt events are optional; missing prompt or compact-summary events simply reduce the available signals. When `--session-id` is omitted, Traceary resolves the active session first and then falls back to the latest matching session in the workspace. `Feedback:` / `Correction:` labels are preserved by mapping them into the current minimal durable-memory taxonomy as `preference` candidates. Persisted candidates go through the same sanitization/redaction path as other durable-memory writes before they are stored.

Useful flags:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--json`

### `traceary memory hygiene scan`

Scan `accepted` durable memories and surface three kinds of hygiene suggestions without mutating the store:

- `redaction_hit` — the current audit redaction rules mask content the stored fact still exposes (for example after the operator added a new extra pattern to `~/.config/traceary/config.json`). The suggestion carries `sanitized_fact` so a follow-up `memory supersede` call has a ready replacement.
- `expiry_candidate` — the memory has not been updated for longer than `--expiry-days`; the operator may want to expire it.
- `duplicate` — two or more `accepted` memories share the same scope + fact pair; one should supersede or expire the other.
- `supersede_candidate` — two accepted memories in the same scope differ in text but share a word-Jaccard similarity at or above `--similarity` (default 0.6). The older memory is the supersede target; the newer memory's fact is the suggested replacement (`replacement_memory_id`, `replacement_fact`, `similarity`).

Useful flags:

- `--workspace` — scope filter (defaults to env/detected workspace; leave empty to scan every scope)
- `--expiry-days` — staleness threshold in days (default 90)
- `--similarity` — word-Jaccard threshold for supersede_candidate detection, between 0.0 and 1.0 (0 uses the built-in default 0.6)
- `--json` — print JSON output with per-suggestion metadata

### `traceary memory hygiene apply`

Commit the lifecycle transition implied by each suggestion for the memories in `--ids`. The usecase re-runs the scan first so stale ids (memories the operator already resolved) land in the failure list instead of silently mutating state. Transitions applied:

- `redaction_hit` → `supersede` with the sanitized fact, inheriting the existing scope / type / refs.
- `expiry_candidate` → `expire` at the current time.
- `duplicate` → `reject` the duplicate copy (pick the id whose partner you want to keep).
- `supersede_candidate` → `supersede` with the newer memory's fact (scope / type / refs inherited from the older memory).

Useful flags:

- `--ids` — comma-separated memory ids (repeatable)
- `--expiry-days` — staleness threshold used by the internal scan (default 90)
- `--json` — print JSON output with per-id transition metadata

### `traceary memory export`

Write the accepted durable memories for the current scope into a host-native instruction file (CLAUDE.md, AGENTS.md, or GEMINI.md). Output is deterministic and idempotent: re-running with unchanged memories produces a byte-identical file, and every Traceary-managed block is bracketed by `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` markers so a later `memory import instructions` run can round-trip without creating duplicates.

Useful flags:

- `--target` — one of `claude`, `codex`, `gemini`
- `--workspace` — scope filter (defaults to env/detected workspace)
- `--out` — output path; pass `-` (or omit) to write to stdout
- `--json` — print a summary of the export result in addition to writing the file

### `traceary memory import instructions`

Read a host instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) and create `candidate` durable memories for every bullet outside the Traceary-managed block. Bullets inside the managed block are already represented in the store via export, so they are intentionally skipped.

Useful flags:

- `--source` — host that wrote the file (`claude` / `codex` / `gemini`)
- `--in` — path to the instruction file
- `--workspace` — scope assigned to imported candidates (defaults to env/detected workspace)
- `--json` — print JSON output

### `traceary memory inbox`

Review the durable-memory inbox. `list` surfaces `candidate` memories together with their evidence / artifact ref counts so a reviewer can judge provenance before accepting. `accept` and `reject` take a comma-separated id list via `--ids` and walk the list in order, returning a per-id success / failure breakdown so a partial batch never hides which entries transitioned.

Useful flags:

- `list` — `--workspace`, `--agent`, `--session-family`, `--type`, `--source` (manual / extracted / imported), `--limit`, `--offset`, `--json`
- `accept` — `--ids id1,id2,...` (repeatable), `--confidence`, `--json`
- `reject` — `--ids id1,id2,...` (repeatable), `--json`

The `--source` filter pairs naturally with the extraction and import paths:

- `--source imported` focuses on memories read from host-native sources such as Codex (see `memory import codex`).
- `--source extracted` focuses on memories `traceary memory extract` produced from session signals.

### `traceary memory import codex`

Import durable-memory candidates from a local Codex memory layout — by default `~/.codex/memories/MEMORY.md`. Only the handbook file is read; raw memories and rollout summaries are intentionally skipped in this release. Each bullet under `## User preferences`, `## Reusable knowledge`, or `## Failures and how to do differently` becomes a `candidate` with `source=imported`, file evidence/artifact refs, and a scope resolved from the Codex `applies_to: cwd=...` hint (falling back to `--workspace` when the source file does not declare one). Sanitization runs on every imported fact, and nothing is auto-accepted. Re-running the command is idempotent: existing candidates (at any lifecycle status, including rejected) suppress a duplicate import so previously reviewed memories are never resurrected.

Useful flags:

- `--root` — Codex memory root (defaults to `~/.codex/memories`)
- `--workspace` — fallback scope when the source file has no `applies_to` hint
- `--watch` — keep polling instead of exiting after one run
- `--interval` — polling interval when `--watch` is set (minimum 1s)
- `--json`

### `traceary memory accept <memory-id>`

Accept a candidate durable memory.

Useful flags:

- `--confidence`
- `--id-only`
- `--json`

### `traceary memory reject <memory-id>`

Reject a candidate durable memory.

Useful flags:

- `--id-only`
- `--json`

### `traceary memory supersede <memory-id>`

Replace an accepted durable memory with a new accepted memory. Omitted `--type` and scope flags inherit from the current memory.

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

### `traceary memory expire <memory-id>`

Expire an active durable memory.

Useful flags:

- `--at`
- `--id-only`
- `--json`

### `traceary memory graph add <from-memory-id> --to <to-memory-id> --relation <type>`

Record a typed relationship between two memories (v0.9.0 graph overlay). See [temporal memory architecture](../architecture/temporal-memory.md) for the relation vocabulary and overlay design.

Useful flags:

- `--to`: target memory ID (required)
- `--relation`: `supersedes` / `contradicts` / `supports` / `related-to` / `causes` (required; unknown values are accepted for forward compatibility)
- `--from`: validity window lower bound (YYYY-MM-DD or RFC3339); defaults to "now"
- `--to-date`: validity window upper bound (exclusive); open-ended when omitted
- `--json`

### `traceary memory graph list`

List edges matching the given filters. Uses the same half-open `[valid_from, valid_to)` semantics as `memory list --as-of`.

Useful flags:

- `--memory-id`: restrict to edges touching this memory (source or target)
- `--relation`: filter by relation type
- `--as-of`: evaluate validity at a given timestamp
- `--limit`
- `--json`

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

### `traceary top`

Show a live, auto-refreshing tree dashboard of active sessions grouped by root session. Rows include the most specific agent/subagent role, recording client, start time, latest activity time, and event count. Idle sessions are dimmed when their latest activity is older than `--idle`; they are not hidden. Press `q` or Ctrl-C to quit. `traceary session tree` remains the static retrospective view.

Useful flags:

- `--workspace`
- `--client`
- `--agent`
- `--idle <duration>` — dim rows older than the threshold without hiding them
- `--snapshot --json` — print a one-shot JSON tree using the same node contract as `traceary session tree --json`
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

Exit codes:

- `0`: all checks are `PASS`
- `1`: at least one check is `FAIL`
- `2`: at least one check is `WARN` and no checks are `FAIL`

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

## Store administration (`traceary store ...`)

Starting with v0.9.0, store administration commands live under the `store` namespace. The old top-level `traceary init`, `traceary backup`, and `traceary gc` still work as deprecated aliases (with a notice) and will be removed in v1.0.

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

### `traceary integration codex install` (deprecated)

Install the packaged Codex plugin from a local repository checkout into:

- `~/.agents/plugins`
- `~/.codex/plugins/cache/...`
- `~/.codex/config.toml`
- `~/.codex/hooks.json`

**Deprecated**: prefer Codex's official `/plugins` flow (run `codex` inside the repository → `/plugins` → `Traceary Plugins` → `Traceary`). This command will be removed no earlier than v0.8.0; see the [Codex plugin guide](../integrations/codex-plugin.md) for the full migration recipe.

Useful flags:

- `--repo-root`
- `--codex-home`
- `--marketplace-root`
- `--traceary-bin`

### `traceary integration codex uninstall`

Remove the Traceary-managed Codex plugin cache, plugin config entry, and hook entries while preserving unrelated local Codex settings. Kept as the recommended cleanup step for users migrating away from the deprecated `install` command above.

Useful flags:

- `--codex-home`
- `--marketplace-root`

### `traceary mcp-server`

Run the MCP server over stdio for AI client integration.

## Related docs

- onboarding and quick start: [`../../README.md`](../../README.md)
- environment variables and runtime assumptions: [`../environment/README.md`](../environment/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
