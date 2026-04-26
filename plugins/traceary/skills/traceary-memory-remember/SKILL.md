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
4. **Call `manage_memory`**:
   - For an explicit "remember this now" verb, use `action="remember"`. The memory lands at `status=accepted` because the user's direct ask **is** the acceptance signal.
   - If you are unsure whether the user wants the fact made permanent right now, use `action="propose"` instead. That lands at `status=candidate` and the operator can review it later via `traceary-memory-review`.

   ```
   manage_memory({
     "action": "remember",
     "type": "constraint",
     "workspace": "<current workspace>",
     "fact": "<short, durable statement>",
     "evidence_refs": [{"kind": "session", "value": "<current session id>"}]
   })
   ```
5. **Inform the operator** which path was used (`accepted` for `remember`, `candidate` for `propose`) and how to undo: `manage_memory({"action": "reject", "ids": ["<id>"]})`.

## Guardrails

- **Explicit ask only.** Do not use this skill on hints, hedges, or generic chat. The trigger requires a direct verb ("remember", "save", "覚えておいて", "メモして").
- **Match the action to the certainty.** A direct "remember X" → `action="remember"` (status=accepted, the explicit verb is the consent). Anything weaker → `action="propose"` (status=candidate) and let `traceary-memory-review` handle acceptance.
- **One scope only.** Send exactly one of `workspace`, `agent`, `session_family`. Sending more than one is a contract violation.
- **No secrets.** Traceary redacts on write, but do not pass secret-shaped values into `fact` or `evidence_refs.value` — keep secrets in tool I/O where redaction is exhaustive.
- **Do not duplicate.** If `query_memory(action="retrieve", workspace=..., query=...)` already returns the same fact, ask the operator before proposing again.
