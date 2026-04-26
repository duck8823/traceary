---
name: traceary-memory-capture
description: DEPRECATED — split into traceary-memory-review (inbox / session summary) and traceary-memory-remember (explicit write). Will be removed in v0.12. Empirically this skill never fired — over 30 days / 16,026 hook events the durable-phrase trigger produced 0 propose calls, so the work moved to hook-driven L2/L3 capture plus the two new skills above. This entry stays only so existing references resolve.
version: 1.1.0
---

# Traceary memory capture (deprecated)

This skill no longer fires by intent. Two replacements:

- **`traceary-memory-review`** — list pending inbox candidates, accept / reject, write a session summary. Triggered by review-intent phrases ("Traceary inbox", "review memory candidates", "session recap").
- **`traceary-memory-remember`** — write durable memory **only** when the user explicitly asks ("remember that", "覚えておいて"). Lands in `status=candidate` for review.

The previous content of this skill is preserved below for reference only. New work should use the two skills above; this file will be removed in v0.12.

---

# Traceary memory capture

Durable memory in Traceary is the cross-session layer: decisions, constraints, lessons, preferences, and artifact pointers that should survive compact / clear / a new session. The MCP `manage_memory(action="propose")` tool records a *candidate* — it does not auto-accept — so using it liberally is safe.

## When to propose

Call `manage_memory(action="propose")` (or `manage_memory(action="remember")` when the user explicitly asks you to remember something now) when you observe any of:

- **decision**: a design / scope / strategy choice the operator just committed to.
- **constraint**: a rule the operator stated (e.g. "never force-push to main", "always wait for CI before merging").
- **lesson**: an error / fix pair worth not repeating (e.g. "mock DB tests masked a migration bug — don't do that again").
- **preference**: an ergonomic choice the operator stated (e.g. "keep PRs under 500 lines").
- **artifact**: a pointer worth reusing next session (e.g. `file:docs/architecture/redaction.md`, `url:https://grafana.internal/...`).

## How to call it

Use the MCP tool with at minimum `type`, a scope field (exactly one of `workspace`, `agent`, or `session_family`), `fact`, and `evidence_refs` (the conversation turn / event / issue that justifies the claim). Example:

```
manage_memory({
  "action": "propose",
  "type": "constraint",
  "workspace": "github.com/org/repo",
  "fact": "Release tags require Multi-AI review before push",
  "evidence_refs": [{"kind": "session", "value": "<current session id>"}]
})
```

For artifact references, include `artifact_refs` instead of (or in addition to) evidence: `{"kind": "file", "value": "docs/architecture/redaction.md"}`.

## Guardrails

- **Never auto-accept.** `manage_memory(action="propose")` leaves the memory in `status=candidate` for human review. Do not call `manage_memory(action="accept")` unless the user explicitly confirms.
- **Do not duplicate known memories.** If a `query_memory(action="retrieve")` call would hit an identical fact, skip proposing again.
- **Do not propose noise.** Temporary session state, WIP context, and in-progress tasks do not belong in durable memory — they belong in session handoff, not memory.
- **Do not propose secrets.** Traceary sanitizes on write, but avoid passing secret-shaped payloads.
