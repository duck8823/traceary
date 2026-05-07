# Temporal memory model — evaluation and minimum viable graph overlay

[日本語](./temporal-memory.ja.md)

Part of #567 · closes the evaluation half of #573.

## Context

v0.8 (#565) added half-open `[valid_from, valid_to)` windows to every accepted memory. Readers can time-travel with `traceary memory list --as-of <date>`, and `memory admin hygiene scan`'s `validity_overlap_supersede` detector uses the same windows to propose replacement chains.

Windows alone answer "what did we believe about X at time T" but they do not record **why** X was superseded or **how** X relates to neighbouring facts. Zep / Letta / Graphiti and similar 2026 temporal memory systems model memories as nodes in a temporal knowledge graph with typed edges, which unlocks queries like:

- "what did we believe about X as of DATE, following the chain of supersedes?"
- "what facts contradict Y?"
- "what decisions depend on fact Z — if Z becomes invalid, which others should be re-evaluated?"

This document is the #573 evaluation: read through the reference designs, judge what actually transfers to Traceary's local-first SQLite model, and ship a minimum viable overlay that we can iterate on.

## What the reference designs look like

### Zep (2024 onward)

- **Temporal knowledge graph as the primary store.** Facts are first-class nodes; edges carry the relationship type and a validity window.
- **Backing store is graph-native** (Neo4j / Graphiti). Hosted offering exists; self-hosted path requires running the graph DB.
- **Core query pattern**: `get facts about subject X valid at time T, following <relation> edges up to depth N`.

### Letta (formerly MemGPT)

- **Block-structured memory tiers** (core / archival / external) — orthogonal to graph modelling, but shows that tier separation + explicit references can stand in for a full graph in many operator workflows.
- **References between blocks are by ID, no typed relationship.**

### Graphiti

- **Open-source library** built on Neo4j, aimed at the Zep-style use case minus the hosted plane.
- **Entity + relationship extraction via LLM pass** is the default ingestion path. Valid_from / valid_to are inferred as the LLM extracts claims.

## What transfers to Traceary

Traceary is **local-first, single SQLite, no hosted plane, no mandatory LLM ingestion pass**. That rules out:

- Running Neo4j / Kuzu / DuckDB-on-top-of-graph just for relationships.
- Any design where ingestion fails without an LLM round-trip.
- Hosted / multi-tenant assumptions about fan-out queries.

But the underlying idea — typed edges between memories with their own validity windows — maps cleanly onto a second SQLite table:

```sql
CREATE TABLE memory_edges (
    id              TEXT PRIMARY KEY,
    from_memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    to_memory_id    TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL,
    valid_from      TEXT NOT NULL,
    valid_to        TEXT,
    created_at      TEXT NOT NULL
);
```

`relation_type` is a stable vocabulary (see below). `valid_from` / `valid_to` are fixed-width 9-digit nanosecond timestamps for the same reason the memories table uses them (#664).

## Decision: adopt as additive overlay

Keep SQLite + the current `memories` table as the canonical fact store. Layer a **typed-edge overlay** on top. Do **not**:

- Move memories into graph primary storage.
- Require LLM extraction.
- Expose a graph DB as a deployment dependency.

The overlay is **opt-in** — a user that never records edges gets zero behaviour change.

## Minimum viable overlay (this release)

### Relation vocabulary (v1)

| Relation | Meaning |
|---|---|
| `supersedes` | `from` replaces `to`. Alias that sharpens the existing `supersedes_memory_id` column — the column captures the chain; the edge captures "this supersede was driven by reason R" once `reason` columns ship. |
| `contradicts` | `from` directly contradicts `to`. Useful for `hygiene scan` to flag conflicting facts. |
| `supports` | `from` provides evidence for `to`. Enables "why do we believe X" chains. |
| `related-to` | Catch-all weak link. Callers should prefer a more specific relation when one fits. |
| `causes` | `from` caused `to`. Scoped to causal / dependency facts. |

Unknown relation types are preserved round-trip but filtered by default views — same pattern as `EventBodyBlockType`'s forward-compat rule.

### Storage

Migration `000013_create_memory_edges.sql` creates the table plus two indexes:

- `idx_memory_edges_from (from_memory_id, valid_from DESC)` for "edges out of X as of T"
- `idx_memory_edges_to (to_memory_id, valid_from DESC)` for the reverse direction

### CLI

- `traceary memory admin graph add <from-memory-id> --to <to-memory-id> --relation <type> [--from <ts>] [--to-date <ts>]`
- `traceary memory admin graph list [--memory-id <id>] [--relation <type>] [--as-of <ts>] [--limit N]`

`--to-date` (not `--to`) is the validity-window upper bound — the flag name avoids colliding with the `--to <memory-id>` target flag on `graph add`. Multi-hop traversal is explicitly deferred; `graph list` only returns direct edges.

### MCP

MCP tools for graph operations are **not shipped in this release**. The overlay is currently CLI-only so the MCP contract can be designed after operator feedback reveals which traversal patterns are actually used. Tracked as a follow-up.

## What's explicitly out of scope

- **Multi-hop traversal beyond depth 1.** Land after real usage shows which chains are needed.
- **Cycle detection.** Opt-in edges can form cycles; we accept them and document that the caller is responsible for sane modelling.
- **LLM-driven edge extraction.** No auto-extraction. Edges are written intentionally, same posture as `memory store remember`.
- **Graph visualization.** Replay HTML does not render edges yet. Add only after operator demand.
- **Edge validity overlap hygiene detector.** Future follow-up once edge histories exist.
- **Edge supersedes semantics.** Edges are append-only in v1; use a new edge with a different `valid_from` to "update" the relationship rather than rewriting the old one.

## Follow-up tickets (created when this ships)

- MCP `memory_graph_add` / `memory_graph_query` tools (CLI-first; add the MCP contract after operator usage stabilizes)
- `memory graph walk` for multi-hop traversal
- `hygiene scan edge_overlap` detector
- Replay HTML edge visualization
- Optional LLM-driven edge inference behind an explicit flag

## Revisit cadence

Revisit this doc at the v0.10 / v1.0 planning gate. If real usage shows the edge overlay is either unused (defer / delete) or bursting at the seams (promote to first-class), adjust scope then — not speculatively.
