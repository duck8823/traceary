# Python dependency inventory and reduction plan

[日本語](./python-dependencies.ja.md)

Traceary's core runtime is Go, but a small set of repository workflows still rely on `python3`.
This guide records where that dependency still exists, which audience it affects, and which migration targets should move first.

## Current policy

- the `traceary` CLI and MCP server should keep working without Python
- no new user-facing install or runtime workflow should introduce a Python prerequisite without an explicit design decision
- maintainer-only Python helpers are still allowed for now, but they should have a documented owner and migration target

## Python-backed surfaces today

### User-facing

There are currently no supported user-facing install or runtime flows that require `python3`.
The only supported Codex install path is Codex CLI's official `/plugins` flow (run `codex` inside the repository → `/plugins` → `Traceary Plugins` → `Traceary`). The `traceary integration codex install` helper was retired in v0.14.0, and the cleanup-only `traceary integration codex uninstall` surface was removed in v0.15.0. Neither retired path depends on `python3`; use `/plugins` plus the Codex plugin guide's manual cleanup steps for legacy state.

### Maintainer-only

The documented repo-tooling migration order is now **complete** — every planned
helper runs through `go run ./cmd/repo-tooling ...` (see the migration order
below). A few release/CI guards remain Python but were outside that plan:

| Surface | Current entrypoint | Used by | Planned direction |
| --- | --- | --- | --- |
| release manifest verification | `python3 scripts/verify_release_manifests.py` | release prep, CI integrations/release jobs | outside the original plan; fold into `cmd/repo-tooling` if revisited |
| removed-alias doc guard | `python3 scripts/verify_docs_no_removed_aliases.py` | CI docs job | outside the original plan |
| release-drafter workflow guard | `python3 scripts/verify_release_drafter_workflow.py` | CI docs job | outside the original plan |

## What is intentionally out of scope

These are *not* part of the Python dependency story this issue is addressing:

- shell wrappers under `scripts/hooks/` — those are tracked separately as hook-runtime cleanup work
- one-off local developer scripts outside the supported maintainer workflow
- third-party client tooling that Traceary does not ship

## Preferred migration order

### 1. Integration verification — ✅ done (v0.20.0)

`scripts/verify_integrations.py` has been replaced by `go run ./cmd/repo-tooling integrations verify` and removed. CI, the Makefile (`integrations/check`, `release/bump`), and the integration smoke test now use the Go entrypoint.

- additional repository verifiers should share the same `cmd/repo-tooling` entrypoint instead of growing one-off helpers

### 2. Changelog and docs verifiers

After integration verification, migrate:

- ~~`scripts/verify_changelog_releases.py`~~ → ✅ `go run ./cmd/repo-tooling release verify-changelog` (done, v0.20.0)
- ~~`scripts/verify_docs_i18n.py`~~ → ✅ `go run ./cmd/repo-tooling docs verify-i18n` (done, v0.20.0)
- ~~`scripts/verify_landing.py`~~ → ✅ `go run ./cmd/repo-tooling docs verify-landing` (done, v0.20.0)

These remain maintainer-only, so correctness matters more than urgency.
If a shared Go verifier exists by then, they should join it rather than becoming separate tools again.

### 3. Version bump helper — ✅ done (v0.20.0)

`scripts/bump_version.py` has been replaced by `go run ./cmd/repo-tooling release bump-version --version X.Y.Z` and removed. `make release/bump` uses the Go command. This completes the documented migration order.

## Repository rules going forward

Until the migrations above land:

1. do not add new user-facing Python helper commands
2. if a new maintainer-only Python helper is truly necessary, document:
   - why Go is not being used yet
   - where the helper is called from
   - what the eventual migration target is
3. prefer expanding an existing verifier over adding another one-off script

## Related docs

- architecture principles: [`../architecture/README.md`](../architecture/README.md)
- Codex integration: [`../integrations/codex-plugin.md`](../integrations/codex-plugin.md)
- release workflow: [`../release/README.md`](../release/README.md)
