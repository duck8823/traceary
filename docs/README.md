# Documentation

[日本語](./README.ja.md)

This page is the detailed docs index for Traceary. Start here when the top-level README is no longer enough.

## Start here

- [CLI reference](./cli/README.md): command-by-command behavior, flags, and output contracts
- [Environment reference](./environment/README.md): environment variables, runtime assumptions, and platform support
- [Storage model](./storage/README.md): SQLite layout, migrations, GC behavior, and what Traceary does not store

## Integrations

- [Native integrations](./integrations/README.md): packaged Claude / Codex / Gemini integration bundles, install flows, and smoke tests
- [Hooks guide](./hooks/README.md): Claude Code / Codex / Gemini hook setup, install flow, and troubleshooting
- [MCP guide](./mcp/README.md): running `traceary mcp-server`, tool surface, and host-client integration notes
- [Interactive ergonomics](./interactive/README.md): current CLI ergonomics decisions and follow-up notes from dogfooding

## Operations

- [Backup guide](./backup/README.md): backup, restore, and machine-migration workflow
- [Operational assumptions](./operations/README.md): SQLite concurrency, hook-state assumptions, and known limits
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

`python3 scripts/verify_docs_i18n.py` enforces the pairing and top-of-file language links in CI.
