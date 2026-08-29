# Shared integration skills

[日本語](./skills.ja.md)

Traceary ships **four** skills, one per job. Each host package under
`integrations/*` and `plugins/traceary` carries the same set. Skills instruct
agents to use the **Traceary CLI** (not MCP) so shell invocations stay
observable.

| Skill | Job | Primary CLI |
| --- | --- | --- |
| `traceary-session-history` | Read prior sessions, events, and audits | `list`, `search`, `context`, `show`, `report` |
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

## How the refine request reaches the agent

When work-based pressure is due, Traceary asks the agent to load
`traceary-session-refine`. Every channel's text starts with
`[Traceary] Session <id>` so the skill's step 1 stays true without a
per-host `SKILL.md` edit.

| Host | Channel | Delivery token |
| --- | --- | --- |
| Claude | `Stop` exit 2 + stderr | `stop_exit_2` |
| Codex | `Stop` exit 2 + stderr (primary); next `UserPromptSubmit` plain-text stdout (second, non-interrupting) | `stop_exit_2` / `additional_context` |
| Kimi | `Stop` exit 2 + stderr | `stop_exit_2` |
| Gemini | `BeforeAgent` `hookSpecificOutput.additionalContext` | `additional_context` |
| Antigravity | `Stop` `{decision:continue,reason}` | `additional_context` |
| Grok | `Stop` `{decision:block,reason}` on stdout, **exit 0**. Shipped; host acceptance unverified (docs.x.ai still says stdout is ignored on passive events). On Grok the skill may only be reachable by user phrasing until a live probe confirms the envelope. | `additional_context` |

Stop-envelope and prompt-context rows share the `additional_context` token;
the channel is recovered from `(client, delivery)`.
