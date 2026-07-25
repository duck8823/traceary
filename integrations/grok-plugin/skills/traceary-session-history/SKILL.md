---
name: traceary-session-history
description: Use when the user asks about prior Traceary sessions, event history, command audits, or what happened earlier in the workspace. Trigger on phrases like "Traceary", "session history", "audit trail", "recent events", or "what happened earlier".
version: 1.1.0
---

# Traceary session history

Use the packaged `traceary` MCP server as the default read path when the user asks about local agent history.

## Staged retrieval

Read history in stages. Start with the narrowest useful metadata query, inspect
only the candidates it identifies, and fetch full detail only after selecting a
single event. The same rule applies to prior sessions, event history, command
audits, and "what happened earlier" questions.

### 1. Discovery — metadata only

Start with the current workspace whenever it is known. Add a time range or
`session_id` when the question provides one, then use `projection="metadata"`
and a small limit (normally `5`). Metadata lets you select event IDs, kinds,
timestamps, and stored-size/truncation facts without reading bodies.

```json
{
  "workspace": "<current workspace>",
  "from": "<known start, if any>",
  "to": "<known end, if any>",
  "projection": "metadata",
  "limit": 5
}
```

Use this shape with `list_events`, or add a narrow literal `query` for
`search`. Omit unknown filters rather than guessing them. Use
`session_status(action="latest")` first when the task is specifically about the
most recent session; use `session_status(action="active")` only when it asks
about an open session.

### 2. Inspection — bounded bodies for selected candidates

After Discovery identifies relevant event IDs or an equivalently narrow
session/time slice, make one bounded read with a **positive** `body_limit` of
`300`–`500` runes. Keep the Discovery filters and reduce the limit further
when one candidate is enough. Do not turn a broad result set into a full-body
read.

```json
{
  "workspace": "<current workspace>",
  "session_id": "<selected session, if known>",
  "body_limit": 400,
  "limit": 1
}
```

Use `get_context` only with this selected and bounded scope when surrounding
events are needed. If its output still leaves several plausible candidates,
refine the metadata search instead of requesting full bodies for all of them.

### 3. Detail — one selected event, with a reason

Retrieve full stored content only after stating why it is needed and selecting
one event ID (or an equivalently single-candidate scope). Prefer the CLI detail
path:

```sh
traceary show <event-id>
```

An MCP full-body read is an exception for the same single selected candidate;
do not use `full_body=true` or `body_limit=0` as the first history query.

## Preferred tools

- `session_status(action="latest")`: most recent session metadata for the current workspace
- `session_status(action="active")`: only when the question is specifically about an open session
- `list_events`: Discovery for recent metadata; bounded Inspection after candidates are known
- `search`: Discovery for a narrow literal query; bounded Inspection after candidates are known
- `get_context`: bounded surrounding context for a selected candidate, never a bare first read

## Guardrails

- Prefer MCP reads before direct SQLite inspection.
- Follow Discovery → Inspection → Detail; scope by workspace first and add time/session filters whenever the question supplies them.
- Keep `projection="metadata"` and a small limit for first reads. A positive `body_limit` is for candidate inspection, not bulk history retrieval.
- Use `traceary show <event-id>` for full detail after a single candidate and a reason have been identified.
- Use `record_event(type="log")` / `record_event(type="audit")` only when the user explicitly wants to record something.
- Automatic hooks already cover session boundaries and shell command audits.
