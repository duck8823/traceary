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

## Evidence refs vs artifact refs

Traceary stores two different kinds of references with a memory:

- **evidence refs** explain why the fact is justified
  - examples: `event:...`, `session:...`, `issue:#462`, `pr:#468`
- **artifact refs** point to things an operator or agent may want to open next
  - examples: `file:docs/release/README.md`, `url:https://...`, `command:go test ./...`

Accepted memories require evidence refs. Artifact refs are optional.

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
- MCP `scan_memory_hygiene`

Scan flags three conditions on `accepted` memories: content the current
redaction rules would mask (`redaction_hit`), stale rows that have not
been updated in longer than `--expiry-days` (`expiry_candidate`), and
scope + fact collisions (`duplicate`). Apply commits the lifecycle
transition implied by each suggestion for the listed memory ids —
`redaction_hit` becomes a supersede with the sanitized fact,
`expiry_candidate` becomes an expire, `duplicate` becomes a reject.
MCP exposes the scanner (read-only) via `scan_memory_hygiene` so agents
can surface hygiene suggestions alongside the inbox review workflow.

### Bridge / export path

Use this when you want Traceary to remain the local source of truth but
still publish the current set of accepted memories into the host's own
instruction file:

- `traceary memory export --target <claude|codex|gemini> --out <path>`
- `traceary memory import instructions --source <...> --in <path>`
- MCP `export_memories` / `import_memory_instructions` (agent-driven)

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

### Context / handoff path

Use these when you want the memory layer folded into a resume-friendly context pack:

- `traceary handoff`
- MCP `session_handoff`
- MCP `memory_pack`

`handoff` returns a working-memory summary for the next session. `memory_pack` is the MCP-oriented equivalent when a client wants a structured bundle that already includes durable memories.

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
- [Event lifecycle](../lifecycle.md)
