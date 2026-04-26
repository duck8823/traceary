# Scheduled Operations

[日本語](./scheduled-tasks.ja.md)

Some maintenance work for Traceary is best done as a low-frequency background task: re-checking host hook surfaces, watching for upstream regressions, refreshing the inbox digest. This page documents the scheduled tasks Traceary suggests setting up for a maintainer-class install.

The tasks below assume **Claude Code's own scheduling** (the `/schedule` skill, which fires Claude Code agent runs on a cron). GitHub Actions is intentionally not the recommended path because it would require an Anthropic API key in the runner; Claude Code's scheduled agents reuse local credentials.

## Daily host hook drift check

**Why.** Host CLIs (Claude Code, Codex, Gemini) ship hook changes between minor releases. The `docs/hooks/host-coverage.md` matrix is curated by hand, so without a periodic refresh it silently goes stale.

**What.** A daily Claude Code agent that:

1. Fetches each host's hook reference page (Claude Code docs, Codex CLI binary strings, Gemini CLI bundled docs).
2. Reads `docs/hooks/host-coverage.md` and parses out the wired / available / unsupported matrix.
3. Diffs the host's documented hooks against the matrix.
4. If new hooks appear or a previously-listed hook is gone, opens a `tech-debt: host hook drift detected — <host> <date>` issue with the diff in the body.
5. If there is no drift, the agent exits silently — no issue noise.

**Recommended schedule.** Daily at 06:00 in the maintainer's local timezone. Pick a time the maintainer is unlikely to be in an active session so the agent's output does not interrupt other work.

**Setup.**

```text
/schedule create
  cron: 0 6 * * *
  prompt: |
    Check Traceary's docs/hooks/host-coverage.md against the upstream
    host hook references. For each host (Claude Code, Codex CLI 0.x,
    Gemini CLI 0.36.x), fetch the reference page or local bundle docs,
    compare against the wired / available / unsupported matrix, and if
    a previously-unseen hook appears (or a listed hook is removed),
    open a `tech-debt: host hook drift detected — <host> YYYY-MM-DD`
    issue against duck8823/traceary with the diff. If there is no
    drift, exit silently.
```

**Verification (no-op run).**

A single manual trigger should either open an issue or finish silently with no errors. Re-running on the same day should not duplicate an existing open issue (the agent should match on title prefix).

## Conventions

- A scheduled agent that has nothing to report **must exit silently** so the operator's inbox does not fill with empty runs.
- Issues opened by scheduled agents always carry the `tech-debt:` prefix and a date stamp so they sort cleanly and are easy to triage.
- Scheduled agents that talk to the Traceary store should use the same DB path as the operator's workstation (`~/.config/traceary/traceary.db`); they do not need to set `TRACEARY_DB_PATH` unless the install is non-default.

## Related

- [Hook coverage matrix](../hooks/host-coverage.md) — what the daily check guards.
- [Hook contract](../hooks/contract.md) — capability tiers per host.
