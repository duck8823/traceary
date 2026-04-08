# Traceary extension

This extension wires Traceary into Gemini CLI through three shared surfaces:

- the `traceary` MCP server for session history and event search
- automatic session-boundary and shell-audit hooks
- helper slash commands (`/traceary-help`, `/traceary-doctor`)

Prefer the packaged MCP tools when the user asks about prior sessions, command audits, or what happened earlier in the workspace.

Use `/traceary-doctor` when the user needs setup or troubleshooting guidance.
