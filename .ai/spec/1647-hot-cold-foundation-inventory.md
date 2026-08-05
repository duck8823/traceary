# Issue #1647: hot/cold foundation extraction and abandoned-gate inventory

## Requirement summary

- Preserve generally reusable storage safety and correctness behavior already merged on `main`.
- Do not merge `feature/1620-same-head-release-gate` or carry its machine-specific fixed runner into the hot/cold design.
- Start every hot/cold implementation issue from current `main` and recreate only the behavior owned by that issue.
- Keep public maintenance surfaces only when they have a stable user outcome and a deterministic behavior test.

## Current-state audit

Audited base: `main` at `745d0176`.

The abandoned branch is 51 commits ahead of this base and mixes production fixes with a fixed copied-store collector, full-history search oracle, and environment-specific release evidence. It has no safe contiguous cherry-pick boundary.

The following abandoned-gate entrypoints are **absent from current `main`** and therefore need no removal commit:

- `cmd/repo-tooling/release_gate_v034*`
- `cmd/store-benchmark/release_gate_phase*`
- `cmd/store-benchmark/fixed_search_oracle.go`
- `presentation/cli/release_gate_phase_v034*`
- `infrastructure/sqlite/release_gate_inspection.go`
- `internal/releasegatev034`
- `scripts/build_v034_fixed_gate.sh`

The complete 59-path branch inventory appears below. It includes added files and modifications to existing wiring; repository conformance must check both rather than only filenames.

No product/test file on `main` is bound to `/private/tmp`, the reviewed copied-store fingerprint, its inode layout, or the fixed 24–25 GiB workstation dataset.

## Branch classification

### Recreate under the owning hot/cold issue

| Behavior discovered on the abandoned branch | New owner | Extraction rule |
|---|---|---|
| Canonical arbitrary-byte preservation | #1649 | Recreate the behavior and tests; do not import fixed-gate code. |
| Safe pre-publication abort | #1651 | Reuse the existing prepared-candidate boundary where applicable. |
| Clean-head, binary-hash, same-commit provenance | #1620 | Define a new segment-gate schema; do not reuse the old v0.34 catalog. |
| Bounded compaction resource evidence and recovery | #1653 | Extract domain/application/infrastructure behavior only after segment eviction is designed. |

### Reevaluate after the new boundary exists

| Behavior | Decision |
|---|---|
| Same-store projection lifecycle fixes | Reevaluate in #1652/#1653 only for compatibility while legacy projection entrypoints remain. Disable/remove obsolete entrypoints instead of extending full-history generation. |
| Shared immutable read pool | Reevaluate in #1652 after the federated reader owns a pinned snapshot boundary. |
| General maintenance-command simplification | Create focused follow-up issues only when a concrete public outcome is removed or replaced. |

### Reject

- Fixed full-history generation/retry and its semantic oracle.
- Old fixed migration/performance/search/replacement phase schemas and collector.
- Workstation-specific paths, fingerprints, capacities, inode claims, and orchestration.
- Blind commit-level cherry-picks from the interleaved branch.

## Maintenance surface inventory

| Surface | User outcome | Decision for v0.34 | Deterministic evidence / next owner |
|---|---|---|---|
| `store init` | Initialize the current local SQLite Hot store | Transitional | #1648 defines whether initialization also creates a Catalog/archive root; #1654 owns the final CLI contract. |
| `store backup create/restore` | Back up or restore the current SQLite file | Transitional | A SQLite-only backup is not complete history after cold segments exist. #1648 defines the complete backup set; #1654 updates the command. |
| `store archive create/verify/restore` including `--delete-after-verify` | Create a portable GC-oriented archive and optionally delete exact live identities | Transitional / reevaluate | It must not become a second canonical archive authority. #1648 decides export-vs-segment semantics; #1653 owns all segment-authoritative deletion. |
| `store gc` | Delete rows selected by the existing retention/GC policy | Transitional / reevaluate | Do not use for hot/cold cutover. #1648 defines authority; #1653 enforces segment-scoped deletion eligibility. |
| `store capacity` | Aggregate size and reclaimability diagnostics | Keep | Metadata-only capacity tests; feeds #1650/#1654. |
| `store compact plan/apply/resume/status/rollback` | Safely reclaim SQLite physical space | Keep | Compaction fault/recovery tests; #1653 owns segment-aware eligibility. |
| `store retention plan/apply/restore` | Explicit policy-driven retention with recovery | Transitional / reevaluate | Canonical deletion must be disabled or segment-gated by #1653; noncanonical retention remains separate. |
| `store retention files plan/apply` | Bounded referenced-file cleanup | Keep | File-retention internal tests; unrelated to canonical segments. |
| `store dedupe content-events` | Explicit duplicate cleanup | Keep | Command tests; unrelated to archive placement. |
| `store workspace-alias` | Manage reviewed workspace aliases | Keep | Command tests; required by search/resume semantics. |
| `store payload-rehearsal` | Rehearse codec migration on an independent copied store | Keep as a compatibility tool; not a v0.34 release gate | Existing copied-store/recovery tests; future codec activation issue owns removal. |
| `store search-projection` | Operate the legacy derived projection | Transitional | #1652 must preserve compatibility during federated rollout and decide disable/removal after segment routing. No full-history completion requirement. |
| `store search-maintenance` | Adopt/retire/restore the legacy projection | Transitional | #1652/#1653 own replacement and eventual removal. Do not extend it for hot/cold operations. |
| hidden hook runtime commands | Stable integration protocol invoked by plugins | Keep | Per-integration hook tests; not an operator maintenance surface. |

Until #1648 decides the authority model, `archive --delete-after-verify`, canonical `gc`, raw-body retention, and SQLite-only backup are not accepted as hot/cold migration, eviction, or complete-backup mechanisms.

## Complete abandoned-branch path/hunk inventory

### Added paths: drop with the abandoned fixed gate

| Path | Decision |
|---|---|
| `.ai/spec/1620-same-head-release-gate.md` | Drop; #1620 receives a new segment-gate schema. |
| `cmd/repo-tooling/release_gate_v034.go` | Drop. |
| `cmd/repo-tooling/release_gate_v034_collect.go` | Drop. |
| `cmd/repo-tooling/release_gate_v034_collect_test.go` | Drop. |
| `cmd/repo-tooling/release_gate_v034_test.go` | Drop. |
| `cmd/store-benchmark/fixed_search_oracle.go` | Drop; #1652 builds a hot/cold parity oracle. |
| `cmd/store-benchmark/provenance_test.go` | Drop; provenance behavior is recreated under #1620. |
| `cmd/store-benchmark/release_gate_phase.go` | Drop. |
| `cmd/store-benchmark/release_gate_phase_test.go` | Drop. |
| `docs/architecture/fixed-search-semantic-oracle.md` | Drop. |
| `docs/architecture/fixed-search-semantic-oracle.ja.md` | Drop. |
| `infrastructure/sqlite/event_bounded_shared_read_internal_test.go` | Defer; recreate only with #1652's reader boundary. |
| `infrastructure/sqlite/fixed_search_read_snapshot.go` | Drop/defer; #1652 owns any pinned federated snapshot. |
| `infrastructure/sqlite/payload_rehearsal_preparation_rollback_test.go` | Recreate only if #1651 needs the behavior. |
| `infrastructure/sqlite/release_gate_inspection.go` | Drop. |
| `internal/releasegateprovenance/provenance.go` | Recreate under #1620 with segment-gate semantics. |
| `internal/releasegateprovenance/provenance_test.go` | Recreate under #1620. |
| `internal/releasegatev034/config.go` | Drop. |
| `internal/releasegatev034/config_test.go` | Drop. |
| `internal/releasegatev034/replacement.go` | Drop; resource rules, if needed, belong to #1653 domain behavior. |
| `presentation/cli/release_gate_phase_v034.go` | Drop. |
| `presentation/cli/release_gate_phase_v034_internal_test.go` | Drop. |
| `scripts/build_v034_fixed_gate.sh` | Drop. |
| `scripts/hooks/fixed_gate_build_test.go` | Drop. |

### Existing files changed only to wire or validate the fixed runner: drop those hunks

| Path | Decision |
|---|---|
| `.goreleaser.yml` | Drop fixed-gate provenance ldflags; normal release configuration remains owned by #1621. |
| `Makefile` | Drop fixed-gate build target. |
| `main.go` | Drop fixed-gate identity endpoint/wiring. |
| `cmd/repo-tooling/release_evidence.go` | Drop v0.34 fixed evidence command wiring. |
| `cmd/store-benchmark/main.go` | Drop fixed phase/identity routing; keep only generic benchmark behavior already on main. |
| `cmd/store-benchmark/search_parity.go` and test | Drop fixed-oracle coupling; reevaluate generic correctness fixes in #1652. |
| `infrastructure/sqlite/sqlite_open_inventory_test.go` | Drop fixed-runner allowlist entries; add new segment stores only under their owning issue. |

### Existing production/test files: recreate by behavior, never cherry-pick wholesale

| Paths | Decision / owner |
|---|---|
| `application/store_compaction.go`, `application/usecase/store_compaction.go`, `application/usecase/store_compaction_fault_test.go`, `domain/compaction_run_test.go`, the `CompactionResourcePlan`/`CompactionResourceEvidence` and unproven-candidate rebuild hunks in `domain/prepared_store_upgrade.go`, `infrastructure/sqlite/compaction_files.go` and tests, `infrastructure/sqlite/compaction_sqlite.go` and tests | Recreate only the reviewed bounded-resource/recovery behavior in #1653. |
| The pre-publication rollback/abort transition hunks in `domain/prepared_store_upgrade.go`, `application/usecase/prepared_store_upgrade.go` and recovery test, `infrastructure/sqlite/payload_rehearsal*.go` and tests | Recreate only segment-publication/pre-abort safety required by #1651. `domain/prepared_store_upgrade.go` is split by behavior/hunk ownership and must never be copied wholesale. |
| `application/types/search_projection.go`, `application/usecase/search_projection_*`, `infrastructure/sqlite/search_projection_rebuild*` | Defer to #1652/#1653 compatibility decision; do not extend full-history generation. |
| `infrastructure/sqlite/database.go`, `event_bounded_datasource.go`, `event_page_datasource.go` | Reevaluate shared-read lifecycle only in #1652. |
| `infrastructure/sqlite/event_search_authority.go`, `event_search_query.go`, `literal_search_query.go` | Recreate semantic/performance fixes only with hot/cold router parity tests in #1652. |
| `infrastructure/sqlite/canonical_event_audit.go` and test | Recreate arbitrary-byte/canonical digest behavior in #1649/#1651. |

This table covers every path returned by `git diff --name-status origin/main...feature/1620-same-head-release-gate`; grouped rows enumerate all remaining modified production/test paths by responsibility.

## Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Abandoned branch | Archived | Reference-only comparison | Never merged or used as release evidence. |
| Foundation behavior | Unowned / assigned | Recreated in an issue-scoped PR | One issue owns one reason to change. |
| Fixed runner machinery | Rejected | None | Cannot enter product or test code. |
| Transitional command | Supported until replacement | Existing behavior only | No new hot/cold responsibility is added. |

## Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| Decide keep/defer/drop | #1647 inventory | Later implementation PRs |
| Segment model and invariants | #1648 | Existing same-store projection types |
| Format, Catalog, migration, routing, eviction | #1649–#1653 | One monolithic release runner |
| Product operations | #1654 | Fixed evidence commands |
| Same-head evidence | #1620 | Local database artifacts |

## Behavior tests for later extraction

| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| No abandoned runner dependency | Current `main` | Fixed-runner path inventory runs | No matching product/test file exists | Repository conformance |
| Issue-scoped extraction | A reusable abandoned-branch behavior | A child implementation is proposed | Diff contains no fixed-runner/environment-specific hunk | PR review |
| Transitional command isolation | Existing legacy search command | Hot/cold features are added | Existing semantics remain or command is explicitly deprecated; no segment orchestration leaks into it | CLI behavior |
| Aggregate privacy | Capacity/segment diagnostic | It runs on copied history | Output contains counts/bytes/status only | Integration |

## TDD / delivery plan

1. Complete this inventory-only PR and close #1647.
2. Create #1648 design artifacts from `main`; no production schema before its design checkpoint.
3. For each accepted foundation, write the new child issue's behavior test first, then recreate only the minimal implementation.
4. Run repository conformance grep in each PR to prevent reintroduction of fixed-runner paths and local data bindings.

## Risks and rollback

- **Risk:** useful production fixes remain only on the abandoned branch. **Mitigation:** the owner table records them; extraction requires a red behavior test and independent review.
- **Risk:** transitional search commands become a second hot/cold control plane. **Mitigation:** #1652 either preserves them unchanged for compatibility or explicitly deprecates/removes them.
- **Risk:** documentation-only classification is mistaken for implementation. **Mitigation:** #1648–#1654 remain separate blocking sub-issues.
- **Rollback:** revert this document if the parent architecture changes; no runtime or persistent data behavior is modified by #1647.

## Validation

- force-add this ignored `.ai/spec` artifact, then run `git diff --cached --check`
- repository inventory confirming the rejected fixed-runner files are absent
- `go test ./...`
- `go vet ./...`
- `go tool golangci-lint run`
