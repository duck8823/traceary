---
name: traceary-memory-remember
description: Use ONLY when the user explicitly asks to remember a specific durable fact, decision, constraint, or preference. Trigger phrases — "覚えておいて", "remember that", "for the record", "save this as", "記憶しておいて", "メモして". Do NOT use for generic notes, status updates, or session summaries. The candidate lands in the review inbox; never auto-accepted.
version: 1.0.0
---

# Traceary memory remember

Use this skill **only** when the operator explicitly asks Traceary to remember a specific durable fact. Generic note-taking, status updates, or session summaries do not belong here — those go to `traceary-memory-review`.

## Canonical contract

Explicit remember requests land as **`status=candidate`** in the review inbox. They are **never auto-accepted**. Durable L3 memory grows only through a later accept step (`traceary-memory-review` or an explicit operator accept).

Use MCP `manage_memory` with **`action="propose"`** for this skill. Do not call `action="remember"` from the agent skill path — that CLI/MCP path accepts immediately and bypasses inbox review.

## Workflow

1. **Confirm scope**. Pick exactly one of:
   - `workspace` — applies to the current repo / workspace identifier (default for repo-scoped facts).
   - `agent` — applies to a specific agent identity across workspaces.
   - `session_family` — applies to a session family (rare; use only when the operator says so).
2. **Pick a memory type** that matches the fact: `decision`, `constraint`, `lesson`, `preference`, `artifact`.
3. **Build evidence_refs**. Each ref has `kind` and `value`. The `kind` is constrained to this enum:

   | `kind` | Typical `value` |
   | --- | --- |
   | `event` | `events.id` (e.g. `evt-abc123…`) |
   | `session` | `sessions.session_id` (e.g. `session-…`) |
   | `url` | `https://…` |
   | `file` | `docs/memory/README.md` |
   | `issue` | `#462` |
   | `pr` | `#468` |

   Unknown values fail with `unknown evidence ref kind: <value>`. At minimum include the current `session` id.

   For optional `artifact_refs[].kind` (used to point at follow-up reading material), the enum is **smaller** — `event` and `session` are NOT allowed:

   | `kind` | Typical `value` |
   | --- | --- |
   | `url` | `https://grafana.internal/...` |
   | `file` | `docs/architecture/redaction.md` |
   | `issue` | `#462` |
   | `pr` | `#468` |
4. **Call `manage_memory` with `action="propose"`** so the row lands at `status=candidate`:

   ```
   manage_memory({
     "action": "propose",
     "type": "constraint",
     "workspace": "<current workspace>",
     "fact": "<short, durable statement>",
     "evidence_refs": [{"kind": "session", "value": "<current session id>"}]
   })
   ```
5. **Inform the operator** that a **candidate** was written for review (`status=candidate`), and how to review or undo: `traceary-memory-review`, or `manage_memory({"action": "accept"|"reject", "ids": ["<id>"]})`.

## Guardrails

- **Explicit ask only.** Do not use this skill on hints, hedges, or generic chat. The trigger requires a direct verb ("remember", "save", "覚えておいて", "メモして").
- **Candidate only.** Every write from this skill is `action="propose"` → `status=candidate`. Never auto-accept; never call `action="remember"` from this skill.
- **One scope only.** Send exactly one of `workspace`, `agent`, `session_family`. Sending more than one is a contract violation.
- **No secrets.** Traceary redacts on write, but do not pass secret-shaped values into `fact` or `evidence_refs.value` — keep secrets in tool I/O where redaction is exhaustive.
- **Do not duplicate.** If `query_memory(action="retrieve", workspace=..., query=...)` already returns the same fact, ask the operator before proposing again.
