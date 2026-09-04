# Search projection rebuild

[日本語](search-projection-rebuild.ja.md)

The search-projection subsystem was deleted in v0.49.0 (#2319). There is no generation lifecycle, no `store compact --projection-rebuild` / `--projection-abort`, and no doctor budget/parked/terminal-rows checks.

`traceary search` now uses the two-tier read path from #2318 (refinement-primary plus an unindexed fallback scan over canonical `events` / `command_audits` / `sessions`). Empty-query filter-only search is a structural scan of those same tables. Historical research notes under `docs/research/search-projection-*` describe the deleted subsystem.
