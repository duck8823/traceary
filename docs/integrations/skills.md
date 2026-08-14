# Shared integration skills

[日本語](./skills.ja.md)

Traceary ships **four** skills, one per job. Each host package under
`integrations/*` and `plugins/traceary` carries the same set. Skills instruct
agents to use the **Traceary CLI** (not MCP) so shell invocations stay
observable.

| Skill | Job | Primary CLI |
| --- | --- | --- |
| `traceary-session-history` | Read prior sessions, events, and audits | `list`, `search`, `context`, `show`, `report`, `session latest`, `session latest --active` |
| `traceary-session-refine` | Write a session refinement so bodies can later be discarded | `session refine` |
| `traceary-memory-review` | Curate the memory inbox; optional inline session recap | `memory inbox list` / `accept` / `reject` (+ interactive `review`) |
| `traceary-memory-remember` | Propose durable memory only on an explicit user ask | `memory store propose` (never `memory store remember`) |

## Content contracts

- **session-history** follows Discovery → Inspection → Detail: small metadata
  reads first, bounded `context`, then `show` for one event.
- **session-refine** requires **Motivation** and **The change**. **How it went**
  is optional. Merge with any previous summary instead of rewriting it.
- **memory-remember** always lands as `status=candidate` for later review.
- **memory-review** never auto-accepts candidates without a per-id operator
  decision.

## Package layout

Each skill is a directory containing `SKILL.md`:

```text
integrations/{claude-plugin,gemini-extension,grok-plugin,kimi-plugin,antigravity-plugin}/skills/<skill>/SKILL.md
plugins/traceary/skills/<skill>/SKILL.md
```

Copies of a given skill are kept byte-identical across hosts. Host-specific
help commands (for example Gemini `/traceary-help` or Codex `/traceary:help`)
are separate from this skill surface and orient on CLI / hooks / doctor.

## Count check

There are four skill names and six host packages → **24** `SKILL.md` files.
There is no `traceary-help` skill.
