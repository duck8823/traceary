# Native agent integrations

[日本語](./README.ja.md)

Traceary ships native integration packages for Claude Code, Codex, and Gemini CLI (legacy).

> **v0.21.0 note:** Gemini CLI is the legacy Google AI agent host. **Antigravity** (`/Applications/Antigravity.app`) is the active successor, with capability detection implemented in #1195. After that investigation (#1196), v0.21.0 **intentionally ships no Antigravity hook, package, or release asset** — no supported public CLI/hook contract is confirmed. See the [Antigravity migration status](./antigravity.md) for what is known locally.

These packages all share the same runtime contract:

- they expect the `traceary` CLI to be installed on `PATH`
- they start the shared local MCP server with `traceary mcp-server`
- they record session boundaries and shell-command audits through packaged hooks
- they keep the underlying SQLite store, CLI flags, and `traceary doctor` flow shared across hosts

## Shared capability surface

| Capability | Shared behavior |
| --- | --- |
| MCP server | exposes the Traceary read/write tools through `traceary mcp-server` |
| Session hooks | records session start/end as Traceary events (Codex `Stop` is a per-turn boundary, not a session end — #1170) |
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
| Gemini CLI | `integrations/gemini-extension/` | Gemini extension archive rooted at `gemini-extension.json` — **legacy compatibility only** as of v0.21.0; not the active delegation path |
| Antigravity | — | No supported public CLI/hook contract confirmed; v0.21.0 intentionally ships no package, hook, or release asset (decided in #1196). `traceary doctor --client antigravity --json` reports the current capability state (#1195). |

## Per-host guides

- [Claude Code plugin](./claude-plugin.md)
- [Codex plugin](./codex-plugin.md)
- [Gemini CLI extension (legacy)](./gemini-extension.md)
- [Antigravity migration status](./antigravity.md)
- [Anthropic native memory tool (experimental)](./anthropic-memory-tool.md)

## Validation and smoke tests

Traceary keeps two validation layers for these packages:

1. structural validation in-repo through `go run ./cmd/repo-tooling integrations verify`
2. local smoke tests for installed CLIs through `./scripts/smoke_test_integrations.sh`

The smoke-test script focuses on the installation surfaces that each host currently exposes. Gemini's link/list flow may open a browser authentication prompt, so it is opt-in for headless release prep:

- Claude Code: marketplace validation + install into a temporary home
- Gemini CLI: authenticated extension validation + link flow in a temporary home when `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` is set
- Codex: structural verification of the plugin manifest (`hooks: "./hooks.json"`, commands, skills) plus the retired-stub probes for both `traceary integration codex install` (v0.14.0 removal) and `traceary integration codex uninstall` (v0.15.0 removal) so the migration hints stay accurate
