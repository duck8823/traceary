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

- `candidate`: extracted or proposed memory that still needs review (the lifecycle status is always `candidate` — earlier docs sometimes used "proposed" interchangeably)
- `accepted`: active memory that should be reused across sessions
- `rejected`: candidate that should not be reused
- `superseded`: older accepted memory replaced by a newer one, or a reviewed candidate replaced by a distilled accepted fact
- `expired`: memory that was valid for a limited period and is no longer active

Only active accepted memories are returned by the default "active memory" paths.

See also [Memory blocks: evaluation and decision](../architecture/memory-blocks.md) for the reasoning behind keeping durable memory classified by `type` + `scope` instead of adding a separate `block` axis.

### Content validity window

Every durable memory carries a content validity window `(valid_from, valid_to)` distinct from the lifecycle `status` and the `expires_at` operation timestamp written by `memory admin expire`:

- `valid_from` — when the fact starts being asserted (defaults to `created_at`)
- `valid_to` — when the fact stops being asserted (`NULL` means open-ended)

Default retrieval (CLI `memory list` / `memory search`, MCP `query_memory(action="retrieve")`, `query_memory(action="pack")`, and `session_status(action="handoff")`) hides memories whose `valid_to` is in the past — you only see what is still asserted as true right now.

To time-travel, pass `--as-of <timestamp>` to CLI list / search, which evaluates `valid_from <= asOf < valid_to` against the supplied point in time. Pass `--include-expired` to bypass the validity filter entirely (e.g. when auditing historical decisions). Neither flag replaces lifecycle `status` filtering — a superseded or rejected memory still needs `--status` to surface.

Set or update the window with:

- CLI: `traceary memory admin set-validity <memory-id> [--from <time>] [--to <time>] [--clear-to]`
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

`artifact_refs[].kind` is a separate enum (defined in [`domain/types/artifact_ref.go`](../../domain/types/artifact_ref.go)). It overlaps with the evidence enum but does **not** include `event` / `session`:

| `kind` | Typical `value` |
| --- | --- |
| `url` | `https://grafana.internal/...` |
| `file` | `docs/architecture/redaction.md` |
| `issue` | `#462` |
| `pr` | `#468` |

## How the memory commands relate

### Manual write path

Use these when a human or agent wants to record a fact deliberately:

- `traceary memory store remember`
- `traceary memory store propose`
- `traceary memory store distill`
- `traceary memory inbox accept`
- `traceary memory inbox reject`
- `traceary memory admin supersede`
- `traceary memory admin expire`

### Extraction path

Use these when Traceary should infer memory candidates from existing session signals:

- `traceary memory admin extract`

Extraction creates memory candidates only. It does not auto-accept memories.

Since v0.11.0, the hook-driven session-end path (`traceary hook session <client> end`), the CLI session-end path (`traceary session --end`), and the MCP `manage_session(action="end")` tool all fire extraction automatically as a best-effort step after the session-end record commits, so the inbox grows without the agent having to ask. The Codex `stop` turn boundary also fires extraction best-effort (Codex has no host session-end signal, so end-only extraction would never run for it — #1170); it runs per turn and the extractor dedupes against existing candidates, so re-firing is safe. Errors there are swallowed so the boundary record is never blocked.

A length-based quality filter routes short memory candidates (under 20 runes; artifact refs are exempt) to `source=extracted-hidden` instead of `source=extracted`. The hidden rows stay in the store for audit but are skipped by the default `traceary memory inbox list` view; `--include-hidden` surfaces them.

Since v0.21.0, only unambiguous unified-diff metadata — hunk headers and git-diff markers (`@@`, `diff --git`, `index …`, `+++ `/`--- `, `Binary files …`) — is **dropped entirely** during auto-extraction, because such lines are never durable prose. Explicit `remember this:` intent always overrides the drop. Every other noise class stays **hidden** (kept for audit and recoverable via `--include-hidden`), not dropped: single `+`/`-` content lines (which can be durable prose starting with a CLI flag or sign, e.g. `-race must be enabled for Go tests`), generated-code markers (detected by a loose substring match that can fire on prose about generated files), standalone commands, review-only conclusions, work declarations, and PR/round chatter. Clear those deliberately with the operator-confirmed cleanup workflow below. Candidates created before this change are not deleted.

#### Candidate hygiene

`traceary sessions --snapshot --json` reports `reliability.memory.candidate_hygiene` counts — `stale_count`, `duplicate_count`, `fragment_like_count`, `extracted_hidden_count`, `likely_actionable_count` — so operators can gauge how much of the candidate backlog is actually worth reviewing (`likely_actionable_count`) versus stale, duplicate, fragment-like, or already-hidden noise. The four flag counts may overlap and are subject to the snapshot scan limit (`scan_limit_reached`). To clear low-value candidates, run the dry-run-first `traceary memory inbox cleanup --quality low` to preview, then add `--apply` to reject the matches (cleanup only rejects candidates; it never deletes or auto-accepts). `traceary memory admin hygiene scan` adds similarity-based duplicate detection beyond the snapshot's exact `duplicate_count` (same scope, memory type, and fact).

#### Context-boundary extraction

`traceary memory admin extract` treats meaningful post-compact summaries and clear/reset-equivalent summary events as extraction inputs. Memory candidates from compact-summary style events use `source=compact-summary` and keep the originating event as evidence, so reviewers can inspect the exact host signal before accepting.

Marker-only lifecycle signals such as `manual`, `clear`, `reset`, or hosts that only notify Traceary that context was cleared do not create memory candidates. In `memory admin extract --debug-signals --json`, those rows appear as `ignored` with `reason=marker_only_context_boundary` (or `pre_compact_snapshot` for pre-compact snapshots). Today Claude post-compact can provide summary text; clear/reset support depends on whether the host supplies a summary body. When a host only emits a marker, Traceary records/debugs the boundary but does not fabricate durable memory.

### Review path

Use these once memory candidates have accumulated in the store and you need to
walk the memory review queue before anything is promoted to `accepted`:

- `traceary memory inbox list`
- `traceary memory inbox accept <id>` (single id; pass `--ids id1,id2,...` for batch scripts; add `--id-only` for scripted callers that want only the memory id on stdout)
- `traceary memory inbox reject <id>` (single id; pass `--ids id1,id2,...` for batch scripts; add `--id-only` for scripted callers that want only the memory id on stdout)
- `traceary memory inbox attach <id> --evidence kind:value` (repeat `--evidence`; optional `--artifact kind:value`) to add supporting refs to a useful candidate before accepting or distilling it. Artifact-only attachment is allowed only when the candidate already has evidence.
- `traceary memory inbox review` — interactive walk-through with the same filters as `inbox list` (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`). Accept / reject reuse the same application use cases as the batch commands; `r` attaches comma-separated evidence refs plus optional `artifact:kind:value` refs to the focused candidate, and `e` opens an edit prompt that requires an operator-authored fact and routes through `traceary memory store distill` (no auto-accept of LLM output). Refuses to start without a TTY and exits with code `2`, pointing at the batch commands above so non-interactive shells branch deterministically.
- MCP `memory_inbox_batch` for agent-driven review

The review path is deliberately scoped to memory candidates so extraction and
import feed the same memory review queue and a single reviewer pass can clear them.

When one or more raw memory candidates are useful as evidence but are not suitable
as accepted facts verbatim, use `traceary memory store distill`. Distillation
requires the operator to supply the final fact, type, and scope explicitly;
Traceary does not perform LLM rewriting or auto-acceptance. The command
creates a new accepted memory with the union of evidence refs and artifact
refs from the source memory candidates, then handles the source memory candidates
according to `--replace=keep|reject|supersede`.

Example:

```sh
traceary memory store distill \
  --from memory-f332...,memory-7f83... \
  --type constraint \
  --workspace github.com/asahi-digital/delivery-platform \
  --fact 'SNS Publish error mapping must not collapse operationally important AWS SDK v2 SNS errors to unknown.' \
  --replace=supersede
```

### Import path

Use this when you want to surface memories written by another local agent as
Traceary memory candidates without merging the underlying stores:

- `traceary memory admin import codex`
- `traceary memory admin import instructions --source <claude|codex|gemini> --in <path>`

### Hygiene path

Use this periodically to keep the accepted layer tidy:

- `traceary memory admin hygiene scan`
- `traceary memory admin hygiene apply --ids id1,id2,...`
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

- `traceary memory admin export --target <claude|codex|gemini> --out <path>`
- `traceary memory admin import instructions --source <...> --in <path>`
- MCP `query_memory(action="export")` / `manage_memory(action="import_instructions")` (agent-driven)

Export always wraps its output in `<!-- traceary-memories:begin:v1 -->` /
`<!-- traceary-memories:end -->` markers so a subsequent `memory admin import
instructions` run round-trips cleanly. Bullets added outside the managed
block by the operator (or the host's own auto-memory feature) land in the
inbox as candidates for review.

Workspace exports include `global` memories by default so user-level
operating rules (for example PR title or review policy) are available
next to repository-specific memories in the generated host file. The
markdown separates `Global memories`, `Workspace memories`, and other
scope groups under distinct headings. Use `--no-global` (or
`include_global=false` over MCP) to preserve the old workspace-only
filter; use `--include-global` to make the default explicit.

Activation can be planned before any host-native file is mutated, then
applied explicitly. The same `--target <codex|claude|gemini>` command set
covers every host:

- `traceary memory admin activate --target <host> --status` — read-only health view
- `traceary memory admin activate --target <host> --dry-run [--diff]` — print the planned content/diff without writing
- `traceary memory admin activate --target <host> --apply` — write the activation target safely

`--root` overrides the activation root and `--path` overrides the activation
target file path. For Claude/Gemini, `--path` points at the host context file
and the external memory file is derived from its directory. Apply is
idempotent (re-running with unchanged accepted memories is a no-op), preserves
user-authored content outside the Traceary-managed regions, refuses unsafe
targets (symlinks, directories, malformed markers, newer marker versions), and
reports the activated memory count. Status reports `missing`, `stale`,
`in_sync`, or `invalid` and emits the exact dry-run/apply commands when the
host needs to be refreshed.
`traceary doctor --client <host>` surfaces the same activation status as a
`<host>-memory-activation` check with actionable remediation.

### Activation strategy by host

Traceary uses three distinct layers:

1. **Accepted memory store** — the local SQLite `memories` aggregate. This is
   the source of truth for reviewed durable facts.
2. **Instruction-file export** — deterministic markdown blocks written by
   `traceary memory admin export --target <claude|codex|gemini>`. Export is the
   portable path for hosts that consume project/user instruction files.
3. **Host-native activation** — a host-specific file/write path that makes the
   accepted store visible to that host's native memory system while preserving
   user-authored content outside Traceary-managed blocks.

v0.13.0 ships full host-native activation for **Codex**, **Claude**, and
**Gemini**. The CLI surface is identical across hosts; only the resolved
target paths and managed-region layout differ.

| Host | Default activation root | Managed file(s) | Strategy |
| --- | --- | --- | --- |
| Codex | `~/.codex/memories` (legacy native memory root) | `~/.codex/memories/traceary.md` (single-file) | Traceary-managed memory file. Apply replaces only the managed block within it and preserves content outside that block. |
| Claude | nearest `.git` ancestor or cwd | `<root>/CLAUDE.md` (host context) + `<root>/.traceary/memories/claude.md` (external memory) | Two-file pair. Apply renders the external memory file first, then writes a small managed import stub (`@./.traceary/memories/claude.md`) into `CLAUDE.md` only when missing or stale. |
| Gemini | nearest `.git` ancestor or cwd | `<root>/GEMINI.md` (host context) + `<root>/.traceary/memories/gemini.md` (external memory) | Two-file pair, same shape as Claude. Apply preserves Gemini's `## Gemini Added Memories` byte-for-byte and only appends or updates the managed import stub. |

The full contract — managed marker layout, status states, root detection,
tracked-file policy, and rejected alternatives — lives in the
[host-native memory activation ADR](../architecture/host-native-memory-activation.md).

#### Why Traceary does not write into host-owned auto-memory sections

Each host already has a memory surface that the host itself reads and writes:

- Claude Code reads/writes `~/.claude/projects/<project>/memory/` as
  host-owned auto memory.
- Gemini's `save_memory` tool appends facts to `~/.gemini/GEMINI.md` under a
  `## Gemini Added Memories` heading.

Traceary deliberately stays out of those regions for v0.13.0:

- The accepted memory store remains the source of truth. Mixing it with
  host-managed facts would couple Traceary's projection to a format the host
  may rewrite at any time.
- User-authored content outside Traceary-managed regions must round-trip
  unchanged through `dry-run`, `apply`, `status`, and `doctor`. The Gemini
  smoke test explicitly asserts that the seeded `## Gemini Added Memories`
  section is preserved byte-for-byte after apply.
- Instead of writing into auto-memory, Traceary appends a small
  Traceary-managed import stub plus an external memory file pair. The host
  loads the external memories through its native markdown import mechanism,
  so refreshes touch the rarely-edited host context file only when the stub
  itself needs to change.

The managed import-stub markers (`<!-- traceary-memory-import:begin:v1 -->`
… `<!-- traceary-memory-import:end -->`) and the external-file markers
(`<!-- traceary-memories:begin:v1 -->` … `<!-- traceary-memories:end -->`)
let `import instructions` and other tooling round-trip cleanly without
duplicating bullets or destroying user-authored prose.

#### Common workflow

1. Inspect status — `traceary memory admin activate --target <host> --status`. Run
   inside the project root for Claude/Gemini so the activation root resolves
   to the nearest ancestor that contains `.git`.
2. Preview the planned changes — `traceary memory admin activate --target <host>
   --dry-run --diff`. The output labels each component (`external memory plan`
   / `host context plan` for two-file targets), so reviewers can see exactly
   which file will change.
3. Apply — `traceary memory admin activate --target <host> --apply`. A second apply
   converges to noop when nothing has changed.
4. Verify — `traceary doctor --client <host>` includes a
   `<host>-memory-activation` check with the same dry-run/apply remediation.

If status returns `invalid`, do not rerun apply blindly. The most common
causes are listed under "Recovering from invalid state" below.

#### Recovering from invalid state

`invalid` means Traceary refuses to write because it cannot safely interpret
the host file or the external memory file. Common causes and the recommended
remediation:

| Cause | Why apply refuses | How to recover |
| --- | --- | --- |
| Target is a symlink or a directory | The safe writer rejects non-regular files to avoid following arbitrary paths during atomic rename | Replace the symlink/directory with a regular file (or remove it), then re-run `--status` |
| Managed block was hand-edited and the markers are duplicated, orphaned, or malformed | Traceary cannot tell which bytes are user-authored | Open the affected file, restore the original markers (or remove the managed region entirely), then re-run `--status` and `--apply` |
| Managed block was written by a newer marker version | A newer Traceary on another machine wrote a contract this binary does not understand; overwriting would silently downgrade it | Upgrade Traceary on this machine, or remove the managed block and re-apply |
| An unmanaged import line outside any Traceary stub already points at the expected `.traceary/memories/<host>.md` | Apply would create a duplicate import | Remove the unmanaged line (or wait for the future explicit adopt workflow) before re-running apply |
| Status reports `invalid` for the host context file but the external file looks fine (or vice versa) | The pair is judged by aggregated state | Inspect the per-component fields in `--json` (`host_context.state`, `external_memory.state`) to identify the offending file before editing |

Import reads local Codex Markdown memories (`~/.codex/memories/*.md` by
default). Legacy `MEMORY.md` keeps the handbook allow-list
(`## User preferences`, `## Reusable knowledge`, and `## Failures and how to
do differently`); additional Markdown shards import bullet/list items under
any heading. Each row lands as a `candidate` with `source=imported` plus
file-level evidence/artifact refs. The sanitizer runs on every imported fact,
nothing is auto-accepted, and dedupe walks every lifecycle status (including
rejected) so memories the operator has already declined are never resurrected
by a later import run.

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

- `traceary session handoff`
- MCP `session_status(action="handoff")`
- MCP `query_memory(action="pack")`

`session handoff` returns a working-memory summary for the next session (the v0.13.x top-level `traceary handoff` alias was removed in v0.14.0). `query_memory(action="pack")` is the MCP-oriented equivalent when a client wants a structured bundle that already includes durable memories.

## Sanitization and redaction

Traceary treats durable memory as persistent context, so extracted or written memory content should go through the existing sanitization / redaction path before it is stored.

That means:

- durable memory is safer than raw shell output for long-lived reuse
- but it is still not a place to intentionally store secrets
- if a fact should stay private, do not promote it into durable memory

## Recommended operator workflow

1. keep raw history in the audit layer through hooks or CLI writes
2. inspect recent work with `traceary tail`, `traceary list`, `traceary search`, or `traceary show`
3. use `traceary memory store remember` for explicit facts you already trust
4. use `traceary memory admin extract` to generate reviewable candidates from session summaries and compact summaries
5. use `traceary session handoff` when the next agent or session should start from a compact context bundle

## Related docs

- [Repository README](../../README.md)
- [CLI reference](../cli/README.md)
- [MCP guide](../mcp/README.md)
- [Hook contract](../hooks/contract.md)
- [Lifecycle events](../hooks/lifecycle-events.md)
- [Event lifecycle](../lifecycle.md)
