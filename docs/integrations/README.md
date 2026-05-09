# Native agent integrations

[日本語](./README.ja.md)

Traceary ships native integration packages for Claude Code, Codex, and Gemini CLI.

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
| Doctor flow | uses `traceary doctor --client <host>` for troubleshooting; add `--fix` to apply safe hook/MCP remediations and `--dry-run` to preview writes |
| Versioning | integration packages are published together with Traceary releases |

## Doctor auto-remediation

`traceary doctor --fix --client <host>` first runs the normal doctor checks, applies only checks that advertise a safe automatic remediation, then runs doctor again and exits with the final report's normal exit-code semantics. A run that fixes every warning/failure exits `0`; remaining guided-only warnings/failures keep the regular non-zero status.

Current automatic fixes cover Traceary-managed hook config installation/upgrade and Traceary MCP registration for supported config files, with a backup written before modifying an existing MCP config. Checks such as PATH problems, plugin version mismatch/cache staleness, double registration, host capability notes, and stale custom binary references are guided-only; `--fix` prints a clear skip note and the suggested command when available. Use `--dry-run` with `--fix` to print `would:` actions without writing files. JSON output includes a `fixes` array with the attempted action and before/after status for each warning/failure.

## Host packages

| Host | Package root | Installed surface |
| --- | --- | --- |
| Claude Code | `integrations/claude-plugin/` | Claude marketplace rooted at `.claude-plugin/marketplace.json` |
| Codex | `plugins/traceary/` | Installed via Codex CLI's official `/plugins` flow against the repo-local marketplace at `.agents/plugins/marketplace.json`; the plugin manifest declares the bundled `hooks.json` so Codex wires session / prompt / audit hooks automatically. The `traceary integration codex install` helper was retired in v0.14.0 and the cleanup-only `traceary integration codex uninstall` was retired in v0.15.0; both are now hidden stubs that point at Codex's official `/plugins` flow plus the manual cleanup steps in [docs/integrations/codex-plugin.md](./codex-plugin.md). |
| Gemini CLI | `integrations/gemini-extension/` | Gemini extension archive rooted at `gemini-extension.json` |

## Per-host guides

- [Claude Code plugin](./claude-plugin.md)
- [Codex plugin](./codex-plugin.md)
- [Gemini CLI extension](./gemini-extension.md)
- [Anthropic native memory tool (experimental)](./anthropic-memory-tool.md)

## Validation and smoke tests

Traceary keeps two validation layers for these packages:

1. structural validation in-repo through `python3 scripts/verify_integrations.py`
2. local smoke tests for installed CLIs through `./scripts/smoke_test_integrations.sh`

The smoke-test script focuses on the installation surfaces that each host currently exposes:

- Claude Code: marketplace validation + install into a temporary home
- Gemini CLI: extension validation + link flow in a temporary home
- Codex: structural verification of the plugin manifest (`hooks: "./hooks.json"`, commands, skills) plus the retired-stub probes for both `traceary integration codex install` (v0.14.0 removal) and `traceary integration codex uninstall` (v0.15.0 removal) so the migration hints stay accurate
