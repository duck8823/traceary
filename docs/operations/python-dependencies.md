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
The primary Codex install path is Codex CLI's official `/plugins` flow (run `codex` inside the repository → `/plugins` → `Traceary Plugins` → `Traceary`). The legacy Codex CLI helpers stay available as a deprecated compatibility path and, like the official flow, do not depend on `python3`:

- `traceary integration codex install` (deprecated; scheduled for removal no earlier than v0.8.0)
- `traceary integration codex uninstall` (kept as the recommended cleanup step for users migrating off the deprecated install)

### Maintainer-only

| Surface | Current entrypoint | Used by | Planned direction |
| --- | --- | --- | --- |
| docs pairing verification | `python3 scripts/verify_docs_i18n.py` | local checks, CI docs job | keep short-term; fold into a Go-based repo verifier later |
| integration package verification | `python3 scripts/verify_integrations.py` | release prep, smoke tests, CI | migrate after the Codex install path |
| changelog coverage verification | `python3 scripts/verify_changelog_releases.py` | release prep, CI docs/release jobs | migrate after integration verification if a shared Go verifier exists |
| landing page version drift verification | `python3 scripts/verify_landing.py` | release prep, CI docs job, release workflow | join the shared Go verifier when it exists (e.g. `go run ./cmd/repo-tooling docs verify-landing`) |
| version bump helper | `python3 scripts/bump_version.py` | release prep | migrate last; low user impact |

## What is intentionally out of scope

These are *not* part of the Python dependency story this issue is addressing:

- shell wrappers under `scripts/hooks/` — those are tracked separately as hook-runtime cleanup work
- one-off local developer scripts outside the supported maintainer workflow
- third-party client tooling that Traceary does not ship

## Preferred migration order

### 1. Integration verification

Once the public Codex flow is moved, the next best return comes from `scripts/verify_integrations.py` because it is used in release preparation, smoke tests, and CI.

Preferred replacement direction:

- `go run ./cmd/repo-tooling integrations verify`
- additional repository verifiers should share the same `cmd/repo-tooling` entrypoint instead of growing one-off helpers

### 2. Changelog and docs verifiers

After integration verification, migrate:

- `scripts/verify_changelog_releases.py`
- `scripts/verify_docs_i18n.py`
- `scripts/verify_landing.py`

These remain maintainer-only, so correctness matters more than urgency.
If a shared Go verifier exists by then, they should join it rather than becoming separate tools again.

### 3. Version bump helper

`scripts/bump_version.py` is useful but low priority.
It should move only after the higher-impact checks above have settled.

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
