---
name: traceary-memory-capture
description: Use when the conversation surfaces a durable decision, constraint, lesson, preference, or project artifact that should outlive the current session. Call Traceary's propose_memory MCP tool so the candidate lands in the review inbox; accept_memory only after the user confirms. Trigger on phrases like "let's always", "never X", "the rule is", "from now on", "remember that", "the decision is", "the constraint is", or when the user explicitly states a preference.
version: 1.0.0
---

# Traceary memory capture

Durable memory in Traceary is the cross-session layer: decisions, constraints, lessons, preferences, and artifact pointers that should survive compact / clear / a new session. The MCP `propose_memory` tool records a *candidate* — it does not auto-accept — so using it liberally is safe.

## When to propose

Call `propose_memory` (or `remember_memory` when the user explicitly asks you to remember something now) when you observe any of:

- **decision**: a design / scope / strategy choice the operator just committed to.
- **constraint**: a rule the operator stated (e.g. "never force-push to main", "always wait for CI before merging").
- **lesson**: an error / fix pair worth not repeating (e.g. "mock DB tests masked a migration bug — don't do that again").
- **preference**: an ergonomic choice the operator stated (e.g. "keep PRs under 500 lines").
- **artifact**: a pointer worth reusing next session (e.g. `file:docs/architecture/redaction.md`, `url:https://grafana.internal/...`).

## How to call it

Use the MCP tool with at minimum `type`, `scope` (workspace by default), `fact`, and `evidence_refs` (the conversation turn / event / issue that justifies the claim). Example:

```
propose_memory({
  "type": "constraint",
  "workspace": "github.com/org/repo",
  "fact": "Release tags require Multi-AI review before push",
  "evidence_refs": [{"kind": "session", "value": "<current session id>"}]
})
```

For artifact references, include `artifact_refs` instead of (or in addition to) evidence: `{"kind": "file", "value": "docs/architecture/redaction.md"}`.

## Guardrails

- **Never auto-accept.** `propose_memory` leaves the memory in `status=candidate` for human review. Do not call `accept_memory` unless the user explicitly confirms.
- **Do not duplicate known memories.** If a `retrieve_memories` call would hit an identical fact, skip proposing again.
- **Do not propose noise.** Temporary session state, WIP context, and in-progress tasks do not belong in durable memory — they belong in session handoff, not memory.
- **Do not propose secrets.** Traceary sanitizes on write, but avoid passing secret-shaped payloads.
