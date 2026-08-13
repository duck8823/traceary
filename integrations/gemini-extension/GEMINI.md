# Traceary extension

This extension wires Traceary into Gemini CLI through shared surfaces:

- automatic session-boundary and shell-audit hooks
- body-free usage capture for Traceary-owned headless runs, plus explicit unavailable observations for interactive `AfterAgent` boundaries
- the Traceary CLI on `PATH` for history, memory, and session refinement
- helper slash commands (`/traceary-help`, `/traceary-doctor`)

Prefer the Traceary CLI when the user asks about prior sessions, command audits, or what happened earlier in the workspace. Use the packaged skills for the four jobs below.

Use `/traceary-doctor` when the user needs setup or troubleshooting guidance.

Skill surface (one skill per job):

- `traceary-session-history` — read prior sessions, events, and audits via the CLI.
- `traceary-session-refine` — write a session refinement (Motivation + The change; optional How it went) with `traceary session refine`.
- `traceary-memory-review` — list / accept / reject the inbox; triggered by review-intent phrases ("Traceary inbox", "review memory candidates", "session recap").
- `traceary-memory-remember` — write durable memory only when the user explicitly asks ("remember that", "覚えておいて"). Lands as `status=candidate` for review via `traceary memory store propose`.
