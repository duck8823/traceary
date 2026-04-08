---
description: Explain the Traceary Codex plugin, its MCP tools, and the built-in hook workflow.
---

# Traceary Plugin Help

## Preflight

1. Confirm `traceary` is on `PATH`.
2. Read `docs/integrations/codex-plugin.md` when the user wants install, update, or uninstall details.

## Plan

Summarize the packaged surfaces and point the user at the shortest useful verification command.

## Commands

Use these commands when the user needs setup or verification:

```bash
traceary version
traceary hooks guide --client codex
traceary doctor --client codex --json
```

If the question is about history or recall, prefer the packaged `traceary` MCP tools instead of direct SQLite access.

## Verification

If you run the doctor command, summarize failed checks first, then warnings, then passes.

## Summary

Return a short answer that covers:

- what the plugin wires automatically
- which command the user should run next
- whether the current setup looks healthy

## Next Steps

- `/traceary:doctor` for troubleshooting
- use the `traceary` MCP tools for session history and event search
