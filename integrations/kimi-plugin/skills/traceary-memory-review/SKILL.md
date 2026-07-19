---
name: traceary-memory-review
description: Use when the user asks to review Traceary memory candidates, pending memory inbox items, or to generate a session summary/handoff from current Traceary context. Trigger phrases — "Traceary inbox", "memory inbox", "pending memories", "review memory candidates", "記憶候補", "メモリ候補", "保留中の記憶", "session recap", "セッションまとめ", "summarize this session for next time". Do not trigger for generic status / cleanup / progress requests unless Traceary memory/session review is explicit.
version: 1.1.0
---

# Traceary memory review

Use this skill when the operator explicitly asks to look at Traceary's pending memory inbox, accept/reject candidates, or to recap the current session. This is a **read + curate** skill — it does not write durable memory on its own. For explicit "remember X" requests use `traceary-memory-remember`.

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
3. **Wait for the operator's decision per memory**. The operator may accept, reject, or ask to re-scope a candidate. Do not assume silence implies accept.
4. **Apply decisions** via `manage_memory`:
   - Accept: `manage_memory({"action": "accept", "ids": ["<id>", ...]})`
   - Reject: `manage_memory({"action": "reject", "ids": ["<id>", ...]})`
   - Re-scope or amend: reject the original, then call `manage_memory({"action": "remember", ...})` (or ask the operator to invoke `traceary-memory-remember`) with the corrected scope / fact text.
5. **(Optional) session recap** when the operator says "session recap" / "summarize for next time": compose a 3–5 sentence summary from the events visible via `get_context` / `query_memory(pack)` and present it inline. Persistent storage of session summaries is wired through compact-summary events (see hook contract); a dedicated MCP write path for `sessions.summary` is not exposed today.

## Human fallback: `traceary memory inbox review`

When the operator would rather drive the review themselves at the terminal — for example because they want to clear the inbox without an agent narrating each candidate, or the agent does not have enough scope to curate confidently — point them at the **interactive CLI**. This is the **preferred human fallback** for inbox review (added in v0.14, see #925).

```sh
traceary memory inbox review
traceary memory inbox review --workspace <workspace> --type preference --limit 10
```

Properties to relay so the operator can decide whether the interactive flow fits:

- TTY-only. Refuses to start without a TTY and exits with code `2`, printing batch-fallback guidance.
- Same filter set as `memory inbox list` (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`).
- Action keys: `a` accept, `x` reject, `s` skip, `e` edit/distill, `v` view evidence, `?` help, `q` quit.
- Accept / reject reuse the same application use cases as `memory inbox accept|reject` — same dedupe, same status transitions.
- Edit/distill **requires the operator to type a new fact** and routes through `traceary memory store distill --replace=supersede`; the candidate's LLM-authored text is never auto-accepted.

## Script / batch fallback: `memory inbox list` + `memory inbox accept|reject`

For non-interactive shells (CI, automation, headless agents, long pipelines) the agent or operator should fall back to the snapshot path. This is **distinct** from the interactive review above — pick this path when there is no TTY, when the ids to act on are already known, or when a script needs deterministic exit codes.

```sh
traceary memory inbox list --workspace <workspace> --json
traceary memory inbox accept <memory-id>           # single id (positional)
traceary memory inbox accept --ids id1,id2,id3     # batch
traceary memory inbox reject <memory-id>
traceary memory inbox reject --ids id1,id2,id3
```

`--id-only` is available on `accept` / `reject` for scripted callers that want only the resulting memory id on stdout.

## Guardrails

- **Read first, write last.** The skill leads with `query_memory(retrieve)` and never accepts memory before the operator has seen the list.
- **Never auto-accept candidate memories unless the user explicitly instructs it.** No silent batch accept, no "looks fine, let me approve them all" without a per-id operator decision. `manage_memory(action="accept")` is one-tap-irreversible from a UX standpoint, and the same rule applies to the CLI fallbacks: do not run `memory inbox accept --ids ...` on the operator's behalf without a direct instruction.
- **Distinguish the two CLI fallbacks.** `traceary memory inbox review` is the human-driven interactive walk; `memory inbox list` + `memory inbox accept|reject` is the script/batch path. Recommend the right one for the operator's environment instead of bundling both.
- **Do not propose new memories here.** That is `traceary-memory-remember`'s job. If the operator says "save this", route to that skill instead of writing yourself.
- **Stay scoped.** Default to the current workspace. Wider scopes need an explicit operator instruction.
- **Skip empty inboxes.** If `query_memory(retrieve, status=["candidate"])` is empty, say so plainly — do not invent placeholder candidates.
