# Repository tooling surface and migration plan

[日本語](./repo-tooling.ja.md)

Traceary's user-facing runtime should stay inside the shipped Go CLI and MCP server.
Maintainer-only repository helpers are a different class of tooling: they are not part of the runtime product surface, but they still need a coherent home.

This guide defines that home, the migration order away from Python helpers, and the rules for adding new repository automation.

## Decision

Maintainer-only repository tooling should converge on a dedicated Go entrypoint:

- `go run ./cmd/repo-tooling ...`

This is the preferred surface for repository verifiers, release-preparation helpers, and structure checks that are not part of the supported end-user runtime.

## Why this should not live in the main `traceary` CLI

The main `traceary` binary is the public runtime entrypoint.
It should contain:

- user-facing CLI commands
- supported hook runtime entrypoints
- the MCP server

Maintainer-only repository automation has different constraints:

- it may assume a Git checkout
- it may validate files that only exist in the repository
- it may be useful in CI or release preparation without being part of the installed product

Keeping those helpers under a separate Go entrypoint avoids mixing runtime behavior with repository maintenance concerns.

## What belongs in repo tooling

Examples that belong under `cmd/repo-tooling`:

- integration package verification
- docs i18n pairing checks
- changelog coverage checks
- release-preparation helpers such as version bumps

Examples that do **not** belong there:

- `traceary hook ...` runtime subcommands
- end-user installation and uninstall flows
- MCP server behavior
- one-off personal scripts that are not part of the supported maintainer workflow

## Planned command shape

The repository should converge on subcommands shaped like these:

- `go run ./cmd/repo-tooling integrations verify`
- `go run ./cmd/repo-tooling docs verify-i18n`
- `go run ./cmd/repo-tooling release verify-changelog`
- `go run ./cmd/repo-tooling release bump-version --version X.Y.Z`

The exact package layout can evolve, but the documented entrypoint should stay singular.

## Migration order

### 1. Integration verification — ✅ migrated (v0.20.0)

First target (done):

- ~~`scripts/verify_integrations.py`~~ → `go run ./cmd/repo-tooling integrations verify`

Why first:

- it is used in CI, smoke tests, and release preparation
- it already validates multiple integration packages and their managed files
- it benefits the most from living next to the Go integration logic it verifies

The Go entrypoint reproduces the Python checks (canonical hook copies, Claude /
Codex / Gemini manifests + managed files, the Codex removed-command stubs, and
docs i18n pairs) and is wired into CI (`.github/workflows/ci.yml`) and the
Makefile (`integrations/check`, `release/bump`). The Python script has been
removed.

### 2. Docs pairing verification

Second target:

- `scripts/verify_docs_i18n.py`

Planned replacement:

- `go run ./cmd/repo-tooling docs verify-i18n`

### 3. Changelog coverage verification

Third target:

- `scripts/verify_changelog_releases.py`

Planned replacement:

- `go run ./cmd/repo-tooling release verify-changelog`

### 4. Landing page version drift verification

Fourth target:

- `scripts/verify_landing.py`

Planned replacement:

- `go run ./cmd/repo-tooling docs verify-landing`

### 5. Version bump helper

Final target:

- `scripts/bump_version.py`

Planned replacement:

- `go run ./cmd/repo-tooling release bump-version --version X.Y.Z`

## Rules for new maintainer automation

Until the migrations above land:

1. do not add new maintainer-only Python helpers unless the issue explicitly records why Go is not being used yet
2. prefer extending an existing verifier over adding a new one-off script
3. document every supported maintainer helper from the docs index and the relevant workflow guide
4. keep user-facing entrypoints in `traceary`; keep repository-only helpers in repo tooling

## Current status

Migration step 1 (integration verification) is done: `go run ./cmd/repo-tooling integrations verify` replaces `scripts/verify_integrations.py` in CI, the Makefile, and the integration smoke test. The remaining helpers listed in [`python-dependencies.md`](./python-dependencies.md) are still Python and migrate in the order above. This page defines the agreed Go destination so that future migrations move toward one consistent surface instead of growing more ad-hoc scripts.

## Related docs

- Python dependency inventory: [`./python-dependencies.md`](./python-dependencies.md)
- release workflow: [`../release/README.md`](../release/README.md)
- integrations overview: [`../integrations/README.md`](../integrations/README.md)
