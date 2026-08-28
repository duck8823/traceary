# Release gates vs measurements

[日本語](./gates.ja.md)

**Issue:** #1873 (follow-up to #1620)

A row is a **gate** only when something evaluates it automatically and a miss can fail a release. CI does that through `go test` on a generated fixture store. Maintainers can also run:

```sh
go run ./cmd/repo-tooling release evaluate-gates --db /path/to/fixture.db
```

The default live path (`~/.config/traceary/traceary.db`) is refused. A fixture or an operator copy is required. The live store is an outlier, not the release corpus (#1863).

## Gates

These seven rows are evaluated. `skip` is not a miss. Six of them are evaluated by `release evaluate-gates` / `go test`; projection rebuild completion is evaluated by `scripts/verify-projection-completion.sh`, whose fixture path runs in CI and whose live path runs on an operator copy before a release.

| Gate | Threshold | How it is measured |
|---|---|---|
| Event-emission amplification | `<= 2.0` | events / (`prompt` + `command_executed`) |
| Whole-store amplification | `<= 3x` | `OperatorCostInspector.Amplification` (resident / retained source bytes) |
| Recent search-index amplification | `<= 4x` | `search_projection_state.recent_amplification_ppm` when a **complete** generation recorded a measured status; otherwise skipped |
| `events.body` duplicate share | `< 5%` | uncompressed plaintext bytes versus one copy per `body_sha256` (strict `< 0.05`) |
| Refinement coverage | `>= 95%` of sessions worth folding | `#1879` `FoldGateInspector` |
| Wake injection | works on every eligible host, within budget | `#1879` `FoldGateInspector` (unmeasured / skip when no eligible host) |
| Projection rebuild completion | `state=complete` with a non-empty `active_generation_id` on a live-store copy | `scripts/verify-projection-completion.sh` (evidence JSON: `wall_seconds`, `transitions`, `final_state_row`, `final_lifecycle_row`, `family_bytes.by_table`, `doctor`) |

Peak resident during a search-index rebuild, and the structural recent-index-tier row, stay with #1751 / #1753. They are not gates here.

## Measurements

These five #1620 absolute byte counts are **not** gates. They have no derivation that makes a specific number correct. They are published with the corpus they came from and never fail a release.

Corpus: **maintainer store 2026-08-11 uncompressed #1620**.

| Measurement | Published illustration |
|---|---|
| Undiscardable growth | `<= 5 KiB` / canonical operation |
| Command record | `<= 4 KiB` / command execution |
| Prompt record | `<= 12 KiB` / user turn |
| Session-tier coefficient | `<= 64 KiB` / session |
| Resident store | `<= 13 KiB` / canonical operation |

## Rebuild

In operator-facing text, **rebuild** means rebuilding the **search-index family** (`traceary store compact --projection-rebuild` / `traceary doctor --fix`). It is not a compile step and not a whole-store rebuild. See [search projection rebuild](../search-projection-rebuild.md).

### Projection rebuild completion

This gate runs on a **copy only**. It refuses the default live path (`~/.config/traceary/traceary.db`) and anything under `~/.config/traceary/`. Family bytes versus the 1,464 MiB target are **recorded, not gated**.

```sh
TRACEARY_NO_AUDIT=1 TRACEARY_DB_PATH="$SCRATCH/traceary-copy.db" \
  scripts/verify-projection-completion.sh \
    --traceary "$SCRATCH/traceary-dev" \
    --out "$SCRATCH/projection-completion.json"
```

Evidence fields: `wall_seconds`, `transitions`, `final_state_row`, `final_lifecycle_row`, `family_bytes.by_table`, and `doctor`. PASS requires `state=complete` and a non-empty `active_generation_id`. A rebuild that does not complete is recorded as FAIL with the lifecycle state and reason — it is not skipped.
