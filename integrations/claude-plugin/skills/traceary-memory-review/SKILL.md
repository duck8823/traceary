---
name: traceary-memory-review
description: Use when the user asks to review Traceary memory candidates, pending memory inbox items, or to generate a session summary/handoff from current Traceary context. Trigger phrases — "Traceary inbox", "memory inbox", "pending memories", "review memory candidates", "記憶候補", "メモリ候補", "保留中の記憶", "session recap", "セッションまとめ", "summarize this session for next time". Do not trigger for generic status / cleanup / progress requests unless Traceary memory/session review is explicit.
version: 1.0.0
---

# Traceary memory review

Use this skill when the operator explicitly asks to look at Traceary's pending memory inbox, accept/reject candidates, or to capture a session summary for handoff. This is a **read + curate** skill — it does not write durable memory on its own. For explicit "remember X" requests use `traceary-memory-remember`.

## Workflow

1. **List candidates**. Call `query_memory` with `action="retrieve"` and `status=["candidate"]`. Default scope is the current workspace; broaden to agent / session_family only if the user says so.
   ```
   query_memory({
     "action": "retrieve",
     "status": ["candidate"],
     "workspace": "<current workspace>",
     "limit": 20
   })
   ```
2. **Present the inbox to the user**. For each candidate include the memory id, type, fact, and the conversation evidence the candidate carries. Do not narrate the JSON verbatim — summarize so a busy operator can decide quickly.
3. **Wait for the operator's decision per memory**. The operator may accept, reject, edit, or ask to re-scope a candidate. Do not assume silence implies accept.
4. **Apply decisions** via `manage_memory`:
   - Accept: `manage_memory({"action": "accept", "memory_ids": ["<id>", ...]})`
   - Reject: `manage_memory({"action": "reject", "memory_ids": ["<id>", ...]})`
   - Edit: ask the operator to confirm the new fact text first, then `manage_memory({"action": "edit", "memory_id": "<id>", "fact": "<new fact>"})`.
5. **(Optional) capture a session summary** when the operator asks for "session recap" / "summarize for next time". Compose a 3–5 sentence summary using only events visible from MCP `get_context` / `query_memory(pack)` and write it back via:
   ```
   manage_session({
     "action": "set_summary",
     "session_id": "<current session id>",
     "summary": "<your short recap>"
   })
   ```

## Guardrails

- **Read first, write last.** The skill leads with `query_memory(retrieve)` and never proposes / accepts memory before the operator has seen the list.
- **No silent batch accept.** Always confirm per-id decisions with the operator. `manage_memory(action="accept")` is one-tap-irreversible from a UX standpoint.
- **Do not propose new memories here.** That is `traceary-memory-remember`'s job. If the operator says "save this", route to that skill instead of writing yourself.
- **Stay scoped.** Default to the current workspace and the current session for summary writes. Wider scopes need an explicit operator instruction.
- **Skip empty inboxes.** If `query_memory(retrieve, status=["candidate"])` is empty, say so plainly — do not invent placeholder candidates.
