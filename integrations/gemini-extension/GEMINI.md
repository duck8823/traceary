# Traceary extension

This extension wires Traceary into Gemini CLI through three shared surfaces:

- the `traceary` MCP server for session history and event search
- automatic session-boundary and shell-audit hooks
- body-free usage capture for Traceary-owned headless runs, plus explicit unavailable observations for interactive `AfterAgent` boundaries
- helper slash commands (`/traceary-help`, `/traceary-doctor`)

Prefer the packaged MCP tools when the user asks about prior sessions, command audits, or what happened earlier in the workspace.

Use `/traceary-doctor` when the user needs setup or troubleshooting guidance.

Memory capture is split into two narrow skills:

- `traceary-memory-review` — list / accept / reject the inbox; triggered by review-intent phrases ("Traceary inbox", "review memory candidates", "session recap").
- `traceary-memory-remember` — write durable memory only when the user explicitly asks ("remember that", "覚えておいて"). Lands as `status=candidate` for review.
