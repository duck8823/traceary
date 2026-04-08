---
name: traceary-session-history
description: Use when the user asks about prior Traceary sessions, event history, command audits, or what happened earlier in the workspace. Trigger on phrases like "Traceary", "session history", "audit trail", "recent events", or "what happened earlier".
version: 1.0.0
---

# Traceary session history

Use the packaged `traceary` MCP server as the default read path when the user asks about local agent history.

## Preferred tools

- `latest_session`: most recent session metadata for the current workspace
- `active_session`: only when the user is asking about the still-open session
- `list_events`: recent history without a keyword filter
- `search`: keyword-driven lookups
- `get_context`: summarize the lead-up around an event

## Guardrails

- Prefer the read tools above before suggesting direct SQLite inspection.
- Do not call `add_log` or `add_audit` unless the user explicitly wants to record a new event.
- Hooks already capture session boundaries and shell command audits, so avoid duplicating those writes manually.
