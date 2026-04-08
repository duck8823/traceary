---
name: traceary-help
description: Explain the Traceary Claude plugin, its MCP tools, and the built-in hook workflow. Use when the user asks about Traceary in Claude Code or explicitly calls /traceary-help.
argument-hint: [topic]
allowed-tools: [Read, Bash]
---

# Traceary help

Use this command to orient the user around the Traceary plugin for Claude Code.

## Workflow

1. Confirm whether `traceary` is available on `PATH`.
2. Summarize the three packaged surfaces:
   - the `traceary` MCP server (`traceary mcp-server`)
   - automatic session/audit hooks
   - troubleshooting through `traceary doctor --client claude`
3. If the user asks for setup or verification steps, prefer these commands:
   - `traceary version`
   - `traceary hooks guide --client claude`
   - `traceary doctor --client claude --json`
4. Keep the answer practical. Do not restate the full plugin manifest.
