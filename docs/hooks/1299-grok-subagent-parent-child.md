# Design note: Grok subagent parent/child hook contract (#1299)

[日本語](./1299-grok-subagent-parent-child.ja.md)

Status: closed as unavailable (live re-probe 2026-07-16)
Risk: Medium (host contract / docs only; no runtime change)

## Requirement

Verify whether Grok Build emits `SubagentStart` / `SubagentStop` payloads with a
parent/child identifier Traceary can wire without synthesizing relationships.

## Live evidence (Grok Build 0.2.101)

Sanitized empty workspace, `grok --permission-mode plan --print`, user prompt
requesting a single subagent spawn:

- Host used the `spawn_subagent` **tool** (recorded as `command_executed` via
  `PostToolUse`).
- No `SubagentStart` or `SubagentStop` hook events were delivered to Traceary.
- No dedicated parent/child identifier field was available on a subagent hook
  payload.

## Decision

- **traceary_support = unavailable** for Grok `SubagentStart` / `SubagentStop`.
- Do **not** invent parent/child mapping from tool names or session IDs alone.
- Keep machine-readable classification in `docs/hooks/host-contract.json`.
- Re-open only when a newer Grok Build live-emits the dedicated hooks with a
  documented identity contract and sanitized fixtures.

## Non-goals

- Enabling external-agent policy changes beyond what the host already allows.
- Fabricating lifecycle events from nested tool audits.
