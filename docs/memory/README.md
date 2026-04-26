# Durable memory guide

[日本語](./README.ja.md)

This guide explains how Traceary's durable-memory layer fits into the broader product model and how the memory-related CLI / MCP surfaces relate to each other.

## Where durable memory fits

Traceary organizes agent context into three layers:

| Layer | Role | Typical data |
| --- | --- | --- |
| Audit / Archive | source-of-truth history for search and inspection | raw events, session boundaries, command audits |
| Working memory | short-lived context assembled for resume / handoff | handoff packs, recent commands, compact summaries |
| Durable memory | facts worth carrying across sessions | decisions, constraints, preferences, lessons, artifact refs |

Durable memory is intentionally small. It is not a transcript dump and it is not a replacement for the audit log.

## Memory lifecycle

Every durable memory has a type, scope, status, confidence, evidence refs, and optional artifact refs.

### Statuses

- `candidate`: extracted or proposed memory that still needs review
- `accepted`: active memory that should be reused across sessions
- `rejected`: candidate that should not be reused
- `superseded`: older accepted memory replaced by a newer one
- `expired`: memory that was valid for a limited period and is no longer active

Only active accepted memories are returned by the default "active memory" paths.

See also [Memory blocks: evaluation and decision](../architecture/memory-blocks.md) for the reasoning behind keeping durable memory classified by `type` + `scope` instead of adding a separate `block` axis.

### Content validity window

Every durable memory carries a content validity window `(valid_from, valid_to)` distinct from the lifecycle `status` and the `expires_at` operation timestamp written by `memory expire`:

- `valid_from` — when the fact starts being asserted (defaults to `created_at`)
- `valid_to` — when the fact stops being asserted (`NULL` means open-ended)

Default retrieval (CLI `memory list` / `memory search`, MCP `query_memory(action="retrieve")`, `query_memory(action="pack")`, and `session_status(action="handoff")`) hides memories whose `valid_to` is in the past — you only see what is still asserted as true right now.

To time-travel, pass `--as-of <timestamp>` to CLI list / search, which evaluates `valid_from <= asOf < valid_to` against the supplied point in time. Pass `--include-expired` to bypass the validity filter entirely (e.g. when auditing historical decisions). Neither flag replaces lifecycle `status` filtering — a superseded or rejected memory still needs `--status` to surface.

Set or update the window with:

- CLI: `traceary memory set-validity <memory-id> [--from <time>] [--to <time>] [--clear-to]`
- MCP: `manage_memory({"action":"set_validity","ids":"<id>","valid_from":"...","valid_to":"..."})`

`--clear-to` / `clear_valid_to` explicitly returns a memory to open-ended validity and is mutually exclusive with supplying a new `valid_to`.

## Evidence refs vs artifact refs

Traceary stores two different kinds of references with a memory:

- **evidence refs** explain why the fact is justified
- **artifact refs** point to things an operator or agent may want to open next

Accepted memories require evidence refs. Artifact refs are optional.

### Evidence ref `kind` enum

The MCP / CLI accept exactly these `kind` values for `evidence_refs[].kind` (defined in [`domain/types/evidence_ref.go`](../../domain/types/evidence_ref.go)). Unknown values fail with `unknown evidence ref kind: <value>`.

| `kind` | Meaning | Typical `value` |
| --- | --- | --- |
| `event` | A recorded `events.id` | `evt-abc123…` |
| `session` | A `sessions.session_id` | `session-…` |
| `url` | A web URL | `https://…` |
| `file` | A repo-relative or absolute file path | `docs/memory/README.md` |
| `issue` | An issue identifier | `#462` |
| `pr` | A pull-request identifier | `#468` |

### Artifact ref `kind` enum

`artifact_refs[].kind` is a separate enum with these values:

| `kind` | Typical `value` |
| --- | --- |
| `file` | `docs/architecture/redaction.md` |
| `url` | `https://grafana.internal/...` |
| `command` | `go test ./...` |

## How the memory commands relate

### Manual write path

Use these when a human or agent wants to record a fact deliberately:

- `traceary memory remember`
- `traceary memory propose`
- `traceary memory accept`
- `traceary memory reject`
- `traceary memory supersede`
- `traceary memory expire`

### Extraction path

Use these when Traceary should infer candidate memories from existing session signals:

- `traceary memory extract`

Extraction is candidate-only. It does not auto-accept memories.

### Review path

Use these once candidates have accumulated in the store and you need to
walk the inbox before anything is promoted to `accepted`:

- `traceary memory inbox list`
- `traceary memory inbox accept --ids id1,id2,...`
- `traceary memory inbox reject --ids id1,id2,...`
- MCP `memory_inbox_batch` for agent-driven review

The review path is deliberately `candidate`-scoped so extraction and
import feed the same inbox and a single reviewer pass can clear them.

### Import path

Use this when you want to surface memories written by another local agent as
Traceary durable-memory candidates without merging the underlying stores:

- `traceary memory import codex`
- `traceary memory import instructions --source <claude|codex|gemini> --in <path>`

### Hygiene path

Use this periodically to keep the accepted layer tidy:

- `traceary memory hygiene scan`
- `traceary memory hygiene apply --ids id1,id2,...`
- MCP `query_memory(action="scan_hygiene")`

Scan flags five conditions on `accepted` memories: content the current
redaction rules would mask (`redaction_hit`), stale rows that have not
been updated in longer than `--expiry-days` (`expiry_candidate`),
scope + fact collisions (`duplicate`), scope-sharing pairs whose facts
are similar enough to be rephrasings of the same idea
(`supersede_candidate`), and `(scope, type)`-sharing pairs whose
explicit temporal validity windows overlap (`validity_overlap_supersede`).
The validity-overlap detector is the more specific signal: a pair that
qualifies for both is reported only under `validity_overlap_supersede`
to keep the reviewer's list free of duplicates. Apply commits the
lifecycle transition implied by each suggestion for the listed memory
ids — `redaction_hit` becomes a supersede with the sanitized fact,
`expiry_candidate` becomes an expire, `duplicate` becomes a reject,
and both `supersede_candidate` and `validity_overlap_supersede` become
a supersede using the newer memory's fact as the replacement. MCP
exposes the scanner (read-only) via `query_memory(action="scan_hygiene")` so agents
can surface hygiene suggestions alongside the inbox review workflow.

### Bridge / export path

Use this when you want Traceary to remain the local source of truth but
still publish the current set of accepted memories into the host's own
instruction file:

- `traceary memory export --target <claude|codex|gemini> --out <path>`
- `traceary memory import instructions --source <...> --in <path>`
- MCP `query_memory(action="export")` / `manage_memory(action="import_instructions")` (agent-driven)

Export always wraps its output in `<!-- traceary-memories:begin:v1 -->` /
`<!-- traceary-memories:end -->` markers so a subsequent `memory import
instructions` run round-trips cleanly. Bullets added outside the managed
block by the operator (or the host's own auto-memory feature) land in the
inbox as candidates for review.

Import reads the local Codex handbook (`~/.codex/memories/MEMORY.md` by
default) and records each bullet under `## User preferences`, `## Reusable
knowledge`, and `## Failures and how to do differently` as a `candidate`
with `source=imported` plus file-level evidence/artifact refs. The sanitizer
runs on every imported fact, nothing is auto-accepted, and dedupe walks
every lifecycle status (including rejected) so memories the operator has
already declined are never resurrected by a later import run.

### Query path

Use these to inspect existing durable memories:

- `traceary memory list`
- `traceary memory search`
- `traceary memory show`

#### Retrieval presets

`memory list` and `memory search` (and MCP `query_memory(action="retrieve")`) accept `--preset <name>` to apply a built-in retrieval shape for a common operator scenario. Explicit `--status` / `--type` flags still win — the preset only pre-populates the defaults.

| Preset | Intent | Defaults applied |
| --- | --- | --- |
| `resume` | "Pick up where I left off." No type restriction — preferences and lessons are as useful as decisions. | `status=accepted` |
| `review` | "What did we decide and what are the constraints?" Filters to long-lived knowledge you expect to reread. | `status=accepted`, `type=decision,constraint,artifact` |
| `incident` | "A failure just happened — what do I need to know?" Includes lessons and constraints on top of decisions. Artifact memories (log paths, dashboards, runbooks) surface here too because incident reviewers want to jump straight to the tooling. | `status=accepted`, `type=decision,constraint,lesson,artifact` |

Examples:

- `traceary memory list --preset review --workspace github.com/org/repo`
- `traceary memory list --preset review --type lesson` — explicit `--type` overrides the preset's default
- MCP: `query_memory({"action":"retrieve","preset":"incident","workspace":"..."})`

### Context / handoff path

Use these when you want the memory layer folded into a resume-friendly context pack:

- `traceary handoff`
- MCP `session_status(action="handoff")`
- MCP `query_memory(action="pack")`

`handoff` returns a working-memory summary for the next session. `query_memory(action="pack")` is the MCP-oriented equivalent when a client wants a structured bundle that already includes durable memories.

## Sanitization and redaction

Traceary treats durable memory as persistent context, so extracted or written memory content should go through the existing sanitization / redaction path before it is stored.

That means:

- durable memory is safer than raw shell output for long-lived reuse
- but it is still not a place to intentionally store secrets
- if a fact should stay private, do not promote it into durable memory

## Recommended operator workflow

1. keep raw history in the audit layer through hooks or CLI writes
2. inspect recent work with `traceary tail`, `traceary list`, `traceary search`, or `traceary show`
3. use `traceary memory remember` for explicit facts you already trust
4. use `traceary memory extract` to generate reviewable candidates from session summaries and compact summaries
5. use `traceary handoff` when the next agent or session should start from a compact context bundle

## Related docs

- [Repository README](../../README.md)
- [CLI reference](../cli/README.md)
- [MCP guide](../mcp/README.md)
- [Hook contract](../hooks/contract.md)
- [Lifecycle events](../hooks/lifecycle-events.md)
- [Event lifecycle](../lifecycle.md)
