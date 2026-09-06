# Release gates vs measurements

[日本語](./gates.ja.md)

**Issue:** #1873 (follow-up to #1620)

A row is a **gate** only when something evaluates it automatically and a miss can fail a release. CI does that through `go test` on a generated fixture store. Maintainers can also run:

```sh
go run ./cmd/repo-tooling release evaluate-gates --db /path/to/fixture.db
```

The default live path (`~/.config/traceary/traceary.db`) is refused. A fixture or an operator copy is required. The live store is an outlier, not the release corpus (#1863).

## Gates

These six rows are evaluated by `release evaluate-gates` / `go test`. `skip` is not a miss. The former projection-rebuild completion gate was retired with the search-projection family (#2319).

| Gate | Threshold | How it is measured |
|---|---|---|
| Event-emission amplification | `<= 2.0` | events / (`prompt` + `command_executed`) |
| Whole-store amplification | `<= 3x` | `OperatorCostInspector.Amplification` (resident / retained source bytes) |
| Recent search-index amplification | `<= 4x` | always skip after #2319 (`recent index family is no longer stored`) |
| `events.body` duplicate share | `< 5%` | uncompressed plaintext bytes versus one copy per identical `body` (strict `< 0.05`) |
| Refinement coverage | `>= 95%` of sessions worth folding | `#1879` `FoldGateInspector` |
| Wake injection | works on every eligible host, within budget | `#1879` `FoldGateInspector` (unmeasured / skip when no eligible host) |

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

## Dogfooding policy

Real-sized dogfood runs are **not** release gates (owner decision 2026-09-06, #2349). The v0.49.0 offline-upgrade dogfood on a 12 GiB operator copy needed ~40 GiB transient space, took ~1 h, and failed with SQLITE_FULL on a typical maintainer machine (#2347). It was effectively a manual e2e test with no reproducibility or provisioning story.

What MUST hold instead, per release:

1. Plugin identity: `scripts/verify-post-upgrade-plugin-refresh.sh` — every installed host package matches the released binary.
2. Live record: `scripts/verify-post-upgrade-live-capture.sh` — every unskipped host records `session_started` + `prompt` into a throwaway store.
3. Read-side guarantee: `scripts/verify-record-search-refine.sh` — synthetic record / search / session-refine / memory round-trip on a bounded throwaway store (under 64 MiB, removed on exit).

A real-sized run is opt-in only: provisioned disk, recorded evidence, never a gate. See [post-upgrade plugin refresh](./post-upgrade-plugins.md).

## Rebuild

The search-projection family was deleted in v0.49.0 (#2319). `store compact --projection-rebuild` / `--projection-abort` are unknown flags. Search uses the two-tier read path. Offline DROP + VACUUM of the old family is `traceary doctor --fix` (verified candidate, never at store open). See [search projection rebuild](../search-projection-rebuild.md).

The #2265 projection-rebuild completion gate (`scripts/verify-projection-completion.sh`) is retired. `WITHOUT ROWID` conversion of `search_projection_session_keywords` and `literal_search_fingerprints` (#2266) stays unshipped.
