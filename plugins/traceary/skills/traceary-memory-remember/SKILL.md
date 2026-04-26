---
name: traceary-memory-remember
description: Use ONLY when the user explicitly asks to remember a specific durable fact, decision, constraint, or preference. Trigger phrases — "覚えておいて", "remember that", "for the record", "save this as", "記憶しておいて", "メモして". Do NOT use for generic notes, status updates, or session summaries. The candidate lands in the review inbox; never auto-accepted.
version: 1.0.0
---

# Traceary memory remember

Use this skill **only** when the operator explicitly asks Traceary to remember a specific durable fact. Generic note-taking, status updates, or session summaries do not belong here — those go to `traceary-memory-review`.

## Workflow

1. **Confirm scope**. Pick exactly one of:
   - `workspace` — applies to the current repo / workspace identifier (default for repo-scoped facts).
   - `agent` — applies to a specific agent identity across workspaces.
   - `session_family` — applies to a session family (rare; use only when the operator says so).
2. **Pick a memory type** that matches the fact: `decision`, `constraint`, `lesson`, `preference`, `artifact`.
3. **Build evidence_refs**. Each ref has `kind` (one of `event`, `session`, `url`, `file`, `issue`) and `value`. At minimum include the current `session` id; add `event` ids, `file` paths, `issue` numbers, or `url`s when they support the fact.
4. **Call `manage_memory`** with `action="remember"`. The candidate lands at `status=candidate`; it is **not** auto-accepted.
   ```
   manage_memory({
     "action": "remember",
     "type": "constraint",
     "workspace": "<current workspace>",
     "fact": "<short, durable statement>",
     "evidence_refs": [{"kind": "session", "value": "<current session id>"}]
   })
   ```
5. **Inform the operator** that the fact landed in the review inbox and can be promoted with `traceary-memory-review` (accept) or rolled back with `manage_memory(action="reject")`.

## Guardrails

- **Explicit ask only.** Do not use this skill on hints, hedges, or generic chat. The trigger requires a direct verb ("remember", "save", "覚えておいて", "メモして").
- **Never accept on the user's behalf.** Always leave the new memory at `status=candidate`. Acceptance happens in `traceary-memory-review`.
- **One scope only.** Send exactly one of `workspace`, `agent`, `session_family`. Sending more than one is a contract violation.
- **No secrets.** Traceary redacts on write, but do not pass secret-shaped values into `fact` or `evidence_refs.value` — keep secrets in tool I/O where redaction is exhaustive.
- **Do not duplicate.** If `query_memory(action="retrieve", workspace=..., query=...)` already returns the same fact, ask the operator before proposing again.
