# Memory blocks: evaluation and decision

[日本語](./memory-blocks.ja.md)

This document evaluates whether Traceary's durable memory should adopt a typed-block axis inspired by letta-ai/claude-subconscious (and similar memory-centric agent systems), and records the decision.

**TL;DR**: **not adopted**. The user-visible outcomes a "blocks" axis promises — *resume*, *review*, and *incident*-style retrieval modes — are already achievable via the existing `type` + `scope` classification combined with the upcoming retrieval presets (v0.8-5, #570). Adding an independent `block` axis would duplicate responsibilities with `type` and increase complexity without a net functional gain.

## What a memory-blocks axis would mean

letta-ai/claude-subconscious and similar projects argue that a single-blob durable memory underperforms a memory split into **typed blocks** such as:

- `guidance` — how the agent should behave
- `preferences` — user preferences
- `project-context` — current project state
- `recurring-patterns` — lessons extracted from repeated workflows
- `unfinished-work` — open tasks to pick up later

The claim is that retrieval quality improves when these block identities are explicit because they gate *how* memory is injected into different situations.

## Traceary's existing axes

Durable memory today is described by four orthogonal axes (schema: `schema/sqlite/migrations/000008_create_memories.sql`):

| Axis | Values | Responsibility |
| --- | --- | --- |
| `type` | `preference`, `decision`, `constraint`, `lesson`, `artifact` | What **kind of knowledge** this memory represents |
| `scope` | `workspace`, `agent`, `session_family` | **Where** this memory is valid |
| `status` | `candidate`, `accepted`, `rejected`, `superseded`, `expired` | The **lifecycle state** of the memory |
| `confidence` | `low`, `medium`, `high`, `verified` | How much to **trust** the memory |

The read contract (`MemoryListCriteria` in `application/types/memory_list_criteria.go`) accepts filters on `scope`, `status`, `type`, and `source`. The current `context_pack_builder.go` consumes that contract but only sets `Scopes` + `Statuses` (accepted-only); richer filters are already expressible and are expected to be used by future retrieval presets (v0.8-5, #570).

## Mapping block proposals onto existing axes

| letta block | Existing Traceary representation |
| --- | --- |
| `guidance` | `type=decision` or `type=lesson`, scope `agent` or `workspace` |
| `preferences` | `type=preference` (direct match) |
| `project-context` | `scope=workspace` (scope already *is* the project-context axis) |
| `recurring-patterns` | `type=lesson` |
| `unfinished-work` | Does not map cleanly — this is a *status* concept ("not yet done"), closer to a pending-task signal than a durable memory |

Four of the five proposed blocks reduce to existing `type` + `scope` combinations. The fifth (`unfinished-work`) is structurally different — it is a task queue, not a durable fact — and belongs outside the durable-memory substrate (audit events, session handoffs, or a dedicated work-queue kind, not a memory block).

## Why adding a `block` axis would hurt

1. **Responsibility duplication with `type`**. `type` is already a classification axis. Overlaying `block` means every candidate memory has two nearly-identical classification decisions to make, and producers (prompt / hook / MCP) would inevitably pick inconsistent values.
2. **Responsibility duplication with `scope`**. `project-context` as a block is just `scope=workspace` renamed. If blocks claim that, `scope` and `block` collide on retrieval intent.
3. **Index pressure**. Indexes today are `(scope_kind, scope_value, status, updated_at, id)` and `(type, status, updated_at, id)`. Introducing a fourth high-cardinality filter would either grow the index set or force a composite that loses specificity.
4. **Migration without clear upside**. A `block` column requires an `ALTER TABLE` migration and back-filling existing rows. The only plausible back-fill reads off `type` (sometimes combined with `scope`) — so for every accepted memory already in the store, `block` is derivable from existing columns. The mapping is many-to-one (e.g. `guidance` could draw from either `type=decision` or `type=lesson`) and partial (`unfinished-work` has no clean source), so the best we can do is fix one rule per `type` and accept some arbitrariness. That the back-fill runs from existing columns alone is the signal: the new axis does not introduce information the old axes lacked.
5. **Host-bridge burden**. Every integration surface (MCP, CLI, bridges to CLAUDE.md / AGENTS.md / GEMINI.md) would need to learn the new axis. For a classification that is redundant with `type`, the churn is not justified.

## What Traceary will do instead

The user-visible behavior memory blocks advertise — *resume my open work*, *review what I decided*, *show everything relevant to this incident* — is delivered more cleanly by **retrieval presets** (v0.8-5, #570):

- **`resume` preset**: recent `type=decision` / `type=lesson` in the session-family scope, plus any open-ended signals from audit events (not from a new memory block).
- **`review` preset**: `type=decision` / `type=constraint` over a wider window.
- **`incident` preset**: time-boxed retrieval pulling `type=lesson`, recent failures, and related decisions — a composite query, not a block.

These presets live in the application layer and do not require schema changes. They can be iterated and tuned per operator feedback without a migration every time.

## Additional consequence for v0.8-3 temporal validity (#565)

Because `unfinished-work` is *not* being modelled as a memory block, its natural home is the pending-work signal in audit events or session summaries. Temporal validity (`valid_from` / `valid_to`) on durable memory therefore does not need to express "this open task is still open" — that is an event-stream concern, not a memory-lifecycle concern. This keeps v0.8-3's design surface smaller.

## Decision

- **Not adopting** a `block` axis on durable memory.
- Instead, proceed with v0.8-5 (#570) **retrieval presets** to cover the resume / review / incident use cases that motivated the proposal.
- Revisit only if a concrete retrieval need emerges that cannot be expressed as a composition of `type`, `scope`, `status`, and `confidence`.

## Related docs

- [Architecture principles](./README.md)
- [Durable memory guide](../memory/README.md)

## References

- letta-ai/claude-subconscious: memory block structure discussion (public repo / blog posts)
- Current durable-memory model: `domain/model/memory.go`, `domain/types/memory_type.go`, `domain/types/memory_scope.go`, `domain/types/memory_status.go`
- Retrieval pipeline: `application/usecase/context_pack_builder.go`, `application/types/memory_list_criteria.go`
- Schema: `schema/sqlite/migrations/000008_create_memories.sql`
- Retrieval-preset follow-up: #570 (v0.8-5)
