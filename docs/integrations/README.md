# Native agent integrations

[日本語](./README.ja.md)

Traceary v0.1.12 introduces native integration packages for Claude Code, Codex, and Gemini CLI.

These packages all share the same runtime contract:

- they expect the `traceary` CLI to be installed on `PATH`
- they start the shared local MCP server with `traceary mcp-server`
- they record session boundaries and shell-command audits through packaged hooks
- they keep the underlying SQLite store, CLI flags, and `traceary doctor` flow shared across hosts

## Shared capability surface

| Capability | Shared behavior |
| --- | --- |
| MCP server | exposes the Traceary read/write tools through `traceary mcp-server` |
| Session hooks | records session start/end (or `Stop` on Codex) as Traceary events |
| Shell audit hooks | records shell-command executions through `traceary audit` |
| Doctor flow | uses `traceary doctor --client <host>` for troubleshooting |
| Versioning | package manifests track the same release tag as the Traceary repository |

## Host packages

| Host | Package root | Install path |
| --- | --- | --- |
| Claude Code | `integrations/claude-plugin/` | Claude marketplace rooted at `.claude-plugin/marketplace.json` |
| Codex | `plugins/traceary/` | Codex repo/local marketplace rooted at `.agents/plugins/marketplace.json` |
| Gemini CLI | `integrations/gemini-extension/` | Gemini extension archive rooted at `gemini-extension.json` |

## Per-host guides

- [Claude Code plugin](./claude-plugin.md)
- [Codex plugin](./codex-plugin.md)
- [Gemini CLI extension](./gemini-extension.md)

## Validation and smoke tests

Traceary keeps two validation layers for these packages:

1. structural validation in-repo through `python3 scripts/verify_integrations.py`
2. local smoke tests for installed CLIs through `./scripts/smoke_test_integrations.sh`

The smoke-test script focuses on the installation surfaces that each host currently exposes:

- Claude Code: marketplace validation + install into a temporary home
- Gemini CLI: extension validation + link flow in a temporary home
- Codex: structural verification from the packaged marketplace and plugin manifests, with an optional runtime probe when a plugin-enabled Codex build is available
