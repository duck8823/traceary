# Native agent integrations

[日本語](./README.ja.md)

Traceary ships native integration packages for Claude Code, Codex, Gemini CLI (legacy), Antigravity, and Grok Build.

> **v0.21.1 note:** Gemini CLI is the legacy Google AI agent host. **Antigravity** (`/Applications/Antigravity.app`) is the active successor. As of v0.21.1, Traceary supports Antigravity as a real hook client with a packaged plugin against the documented public hook surface. See the [Antigravity hooks and plugin guide](./antigravity.md).

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
| Codex | `plugins/traceary/` | Installed via Codex CLI's official `/plugins` flow against the repo-local marketplace at `.agents/plugins/marketplace.json`; the plugin manifest declares the bundled `hooks.json` so Codex wires session / prompt / audit hooks automatically. The legacy `traceary integration` command tree (including codex install/uninstall stubs) was fully removed in v0.25.0 (#1266); use Codex's official `/plugins` flow plus the manual cleanup steps in [docs/integrations/codex-plugin.md](./codex-plugin.md). |
| Gemini CLI | `integrations/gemini-extension/` | Gemini extension archive rooted at `gemini-extension.json` — **legacy compatibility only** as of v0.21.0; not the active delegation path |
| Antigravity | `integrations/antigravity-plugin/` | Supported in v0.21.1. Direct hook installs target `<project>/.agents/hooks.json` or `~/.gemini/config/hooks.json`; the packaged plugin adds a versioned manifest, Traceary MCP server, and three shared memory/session skills. `traceary doctor --client antigravity --json` reports hook routes, MCP registration, and plugin version parity. |
| Grok Build | `integrations/grok-plugin/` | Supported in v0.23.0. The native plugin contains seven live-verified lifecycle hooks, one Traceary MCP server, and three shared memory/session skills. Install it with `scripts/install-grok-plugin.sh`, then verify the installed hook contract, trust, inventory, and version parity with `traceary doctor --client grok --json`. |

## Per-host guides

- [Claude Code plugin](./claude-plugin.md)
- [Codex plugin](./codex-plugin.md)
- [Gemini CLI extension (legacy)](./gemini-extension.md)
- [Antigravity hooks and plugin](./antigravity.md)
- [Grok Build plugin](./grok-plugin.md)
- [Anthropic native memory tool (experimental)](./anthropic-memory-tool.md)

## Validation and smoke tests

Traceary keeps two validation layers for these packages:

1. structural validation in-repo through `go run ./cmd/repo-tooling integrations verify`
2. local smoke tests for installed CLIs through `./scripts/smoke_test_integrations.sh`

The smoke-test script focuses on the installation surfaces that each host currently exposes. Gemini's link/list flow may open a browser authentication prompt, so it is opt-in for headless release prep:

- Claude Code: marketplace validation + install into a temporary home
- Gemini CLI: authenticated extension validation + link flow in a temporary home when `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` is set
- Codex: structural verification of the plugin manifest (`hooks: "./hooks.json"`, commands, skills) plus a probe that the removed `traceary integration` subtree fails as an unknown command (v0.25.0, #1266)
- Grok Build: native package validation plus install, inventory inspection, and uninstall in a temporary home
