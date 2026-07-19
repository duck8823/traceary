---
name: traceary-session-history
description: Use when the user asks about prior Traceary sessions, event history, command audits, or what happened earlier in the workspace. Trigger on phrases like "Traceary", "session history", "audit trail", "recent events", or "what happened earlier".
version: 1.0.0
---

# Traceary session history

Use the packaged `traceary` MCP server as the default read path when the user asks about local agent history.

## Preferred tools

- `session_status(action="latest")`: most recent session metadata for the current workspace
- `session_status(action="active")`: only when the question is specifically about an open session
- `list_events`: quick recent history without a keyword query
- `search`: keyword-driven lookups
- `get_context`: summarize the lead-up around an event

## Guardrails

- Prefer MCP reads before direct SQLite inspection.
- Use `record_event(type="log")` / `record_event(type="audit")` only when the user explicitly wants to record something.
- Automatic hooks already cover session boundaries and shell command audits.
