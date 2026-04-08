---
description: Diagnose the current Traceary + Codex plugin setup and summarize the result.
---

# Traceary Doctor

## Preflight

1. Confirm `traceary` is on `PATH`.
2. If the current workspace is not the Traceary repository, keep the diagnosis focused on runtime health instead of development files.

## Plan

Run the Traceary doctor command for Codex and summarize the result in order of severity.

## Commands

```bash
traceary doctor --client codex --json
```

If the user asks for a human-readable rerun, you may also use:

```bash
traceary doctor --client codex
```

## Verification

- Parse the JSON output when available.
- Group checks into `fail`, `warn`, and `pass`.
- Mention the DB path, hook script location, and Codex config status when they appear in the report.

## Summary

Return:

- overall status
- failing checks
- warnings worth acting on next

## Next Steps

Recommend the smallest next command that moves the user forward.
