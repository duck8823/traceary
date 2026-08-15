# `sessions.summary` is not a summary source

[日本語](./sessions-summary-retirement.ja.md)

#1706 retires `sessions.summary` as a reader-facing session summary.

## Decision

- Readers (session list / tree / lineage, and handoff via that list) use `session_refinements.summary`.
- `session end --summary` writes a refinement (`produced_by=cli:session-end`, `covers_to` = the session-ended event).
- `SetSummaryIfEmpty` is removed. Post-compact already wrote a refinement; it no longer dual-writes the column.
- The column stays. Dropping it is a data-dependent offline migration; implicit migrate on a populated store is refused (#1852). Historical leftovers remain unread.

## Why keep the column

A DROP would force `traceary store init` on every store that already has events. That is not a v0.36 implicit-open change.
