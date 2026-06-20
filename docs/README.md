# Documentation

[日本語](./README.ja.md)

This page is the detailed docs index for Traceary. Start here when the top-level README is no longer enough.

## Start here

- [Architecture principles](./architecture/README.md): layering rules, runtime boundaries, the role of `scripts/`, and the current `internal/` stance
- [Optional API migration policy](./architecture/optional-api.md): current Optional[T] gap, target convention API, and staged rollout
- [Host-native memory activation contract](./architecture/host-native-memory-activation.md): v0.13.0 Claude/Gemini activation target, import-stub, marker, and safety contract
- [Durable memory guide](./memory/README.md): the three-layer model, memory lifecycle, refs, and how memory commands relate
- [CLI reference](./cli/README.md): command-by-command behavior, flags, and output contracts
- [CLI stability and deprecation policy](./cli-stability.md): public / admin / plumbing tiers, deprecation notice expectations, and v0 vs v1 removal policy
- [Hook contract](./hooks/contract.md): automatic-capture coverage and shared hook semantics across hosts
- [Event lifecycle](./lifecycle.md): how session starts, audits, prompts, and summaries become Traceary events
- [Environment reference](./environment/README.md): environment variables, runtime assumptions, and platform support
- [Storage model](./storage/README.md): SQLite layout, migrations, GC behavior, and what Traceary does not store

## Integrations

- [Native integrations](./integrations/README.md): packaged Claude / Codex / Gemini (legacy) integration bundles, install flows, and smoke tests
- [Antigravity hooks and plugin](./integrations/antigravity.md): v0.21.1 hook client support, packaged plugin, install paths, limitations, and the official hook/plugin references
- [Hooks guide](./hooks/README.md): Claude Code / Codex / Gemini (legacy) hook setup, install flow, and troubleshooting
- [MCP guide](./mcp/README.md): running `traceary mcp-server`, tool surface, and host-client integration notes
- [Interactive workflows](./interactive/README.md): how to inspect live and recent activity with `list`, `tail`, `search`, `show`, and `handoff`

## Operations

- [Backup guide](./backup/README.md): backup, restore, and machine-migration workflow
- [Operational assumptions](./operations/README.md): SQLite concurrency, hook-state assumptions, and known limits
- [Python dependency plan](./operations/python-dependencies.md): current Python-backed helpers, audience impact, and reduction order
- [Repository tooling plan](./operations/repo-tooling.md): the planned Go entrypoint for maintainer-only repository helpers
- [Cockpit UI/UX design baseline](./operations/cockpit-ui-ux-design.md): v0.18 reference-driven redesign target for `traceary tui`
- [Memory command surface plan](./operations/memory-command-surface.md): v0.14 plan for `memory inbox`, `memory store`, and `memory admin` plus the hidden deprecated aliases retained until v0.15
- [Release guide](./release/README.md): release packaging, GitHub Actions, and local snapshot builds

## Project docs

- [Repository README](../README.md): install, quick start, and core commands
- [Contributing guide](../CONTRIBUTING.md): local checks, PR expectations, and security reporting path
- [Changelog](../CHANGELOG.md): release-by-release history

## Documentation rules in this repository

Human-facing Markdown is maintained in English/Japanese pairs.

- use the English filename as the default
- add the Japanese variant with a `.ja.md` suffix
- update both language variants in the same pull request
- keep the language switch link near the top of each paired document

`go run ./cmd/repo-tooling docs verify-i18n` enforces the pairing and top-of-file language links in CI.
