# Decision: measure the two v0.34-unmeasurable fold/wake rows (#1879)

[日本語](./fold-gate-measurement.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1879

## Sessions worth folding

A session is **worth folding** when either:

1. it has a `session_refinements` row, or
2. `SUM(event_metadata_projection.body_stored_bytes) >= consolidation.threshold_bytes` (default 64 KiB).

(1) keeps sessions that already folded and later discarded bodies. (2) is the same threshold crossing that fires the stop-hook ask.

`refinement_ratio = refinement_count / worth_folding_count`. The v0.34 gate is `>= 0.95`.

## Wake injection

Per `sessions.client`, not in aggregate. Eligible means top-level session + `has_agent_reasoning = 1` (the #1877 rule). A host **injects** when at least one eligible summary fits `wake_injection.budget_bytes` (default 8 KiB). Antigravity is still out of the injection product; a missing client is `unmeasured`, not a fail.

## Summary content

#1874 asked agents to state motivation and the change. This harness does **not** parse that semantically. It samples the newest 20 agent-authored summaries and reports nonempty / mechanical-template / `content_proxy_ok` (nonempty, not the mechanical header, ≥ 40 bytes).

## Live store

This measurement requires an explicit `--db` copy. The default live path is refused. The last honest figure on the reference store at the v0.34.0 tag remains **0 refinements / 27,552 sessions / 0 wake-eligible rows**. A scratch fixture proves the path; it is not that corpus.

## Non-goals

- Changing consolidation or wake behavior.
- Closing #1873 (v0.39: evaluate every gate threshold in CI).
