# Decision: what a workspace conflict is for (#1768)

[日本語](./workspace-conflict-meaning.ja.md)

**Status:** decided. Keep `store workspace-alias`. Count actionable pairs.

**Date:** 2026-08-15

**Issue:** #1768

## Decision

A workspace `conflict` is an operator-reviewable pair `(session_id, effective workspace)` whose effective value is known, differs from the session canonical workspace, and is not `exact`, `descendant`, `ancestor`, or a reviewed `explicit_alias`.

It is not a store defect and not a reason to retire `traceary store workspace-alias`. The classifier is doing what the contract says: remote identities and local paths stay distinct until an operator reviews them; two unrelated local trees stay distinct.

`report workspace-identity` keeps observation-row counts as volume. It now also reports **distinct conflict pairs** and samples **one row per pair** (with the workspace) so the remedy is reachable.

## Answers

### 1. Is a conflict actionable?

Yes. The review unit is one pair, not one observation row.

| Shape | Why it is `conflict` | Right action |
|---|---|---|
| Remote identity vs local path (`github.com/org/repo` vs `/abs/path`) | Contract forbids auto-alias | Alias if they are the same checkout; leave if they are not |
| Two local trees that are not ancestor/descendant | True cross-workspace event | Leave as conflict, or alias if it is a known worktree |
| Same pair repeated on every `post_tool_use` | Hook frequency, not a new problem | Review the pair once |

Antigravity contributing many distinct pairs while Codex contributes many rows is hook cadence, not two classifiers. `post_tool_use` writes an observation per tool call; `stop` / `stop_transcript` write once per turn. Source already explains that split. This issue does not query the live store.

### 2. Should the report count pairs instead of rows?

It should count **both**. Observation-row totals stay; they are volume, not a deduplication decision (see the contract). Distinct `(session_id, workspace)` pairs under the current `conflict` projection are the actionable count. Samples are one latest observation per pair and include `workspace` so `doctor --alias-add --session … --workspace …` can be run from the report. The management surface moved from `store workspace-alias` in v0.42.0 (#2075).

Row-based `conflict_rate` is unchanged.

### 3. What happens to `store workspace-alias`?

v0.42.0 (#2075) moved the management surface to `doctor --alias-add` / `--alias-remove` / `--alias-list`. The alias rows and conflict contract below are unchanged.

Keep the reviewed-alias mechanism. It is the only public way to add, withdraw, or list a reviewed alias. Existing aliases keep meaning on read (`explicit_alias`) and on future writes. Auto-normalising remotes to paths, or a family-wide rule, would violate the contract. Withdrawing the conflict contract would freeze the mechanism with no replacement.

## Non-goals

- Dropping the reviewed-alias mechanism (the CLI name `store workspace-alias` was folded in #2075).
- Querying or rewriting the live store.
- Auto-aliasing remotes to checkouts.
- Changing row-based `relationships.conflict` or `conflict_rate`.
