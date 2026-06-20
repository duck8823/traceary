# Antigravity migration status

[日本語](./antigravity.ja.md)

Antigravity is Google's successor to Gemini CLI as an AI agent host. This page describes what Traceary has discovered locally about Antigravity in v0.21.0 and the resulting decision.

> **Summary:** No supported public CLI/hook contract for Antigravity has been confirmed in v0.21.0. Antigravity capability detection is implemented in #1195. After that investigation, **v0.21.0 intentionally ships no Antigravity hook, package, or release asset** — Traceary will not fabricate a hook contract that Google has not published. Future support requires a supported public CLI/hook contract. The existing [Gemini CLI extension](./gemini-extension.md) remains available for existing Gemini CLI installs.

## Local discovery (v0.21.0)

The following was observed in the local development environment:

| Property | Value |
| --- | --- |
| Application path | `/Applications/Antigravity.app` |
| Bundle ID | `com.google.antigravity` |
| Version | 2.1.4 |
| URL scheme | `antigravity://` |
| CLI on PATH | Not found |
| User data directory | `~/Library/Application Support/Antigravity` |
| State hints | `~/.gemini/antigravity`, `~/.gemini/config/config.json` |

## Capability detection (v0.21.0)

`traceary doctor --client antigravity --json` probes for Antigravity installation and reports one of four capability states:

| State | Meaning |
| --- | --- |
| `not_installed` | No app bundle (`/Applications/Antigravity.app`) and no `antigravity` CLI found on PATH |
| `tool_unavailable` | App or CLI found but no supported public headless/hook/package surface is confirmed |
| `not_authenticated` | Installed with a supported surface but not authenticated or configured (future/reserved; not reachable in v0.21.0 — detected via a supported CLI/contract check, not by reading credentials) |
| `available` | Supported CLI/hook contract confirmed and configured (not yet reachable in v0.21.0) |

In the local development environment, the current state is **`tool_unavailable`**: `/Applications/Antigravity.app` (version 2.1.4) is installed, but no public CLI or hook contract is confirmed. Run:

```sh
traceary doctor --client antigravity --json
```

This check does not launch the app, perform browser automation, or read credentials — it only checks for the presence of the app bundle and a CLI binary on PATH.

Antigravity is not included in the default doctor client list (`["claude","codex","gemini"]`). Pass `--client antigravity` explicitly.

## What is not confirmed in v0.21.0

- No public CLI binary or hook contract is confirmed. There is no `antigravity` command on PATH.
- No extension/plugin install mechanism equivalent to `gemini extensions install` has been confirmed.
- No hook event schema (session lifecycle, tool audit, prompt capture) has been established for Traceary.

## Decision and follow-up

- **#1195** ✓ — Antigravity capability detection (`traceary doctor --client antigravity --json`) — implemented in v0.21.0
- **#1196** ✓ — Decision: v0.21.0 intentionally ships **no** Antigravity hook, package, generated metadata, or release asset. The acceptance criteria allowed a package **only if** a supported public hook/plugin/MCP/headless CLI surface was confirmed; #1195 confirmed none, so Traceary documents the intentionally omitted package and keeps the doctor `tool_unavailable` state instead of shipping a fake package.

A real Antigravity package will be added under a future issue **only once** Google publishes a supported public CLI/hook contract. Until then, Antigravity sessions will not appear in Traceary's event log. If you are migrating from Gemini CLI to Antigravity, continue using the [Gemini CLI extension](./gemini-extension.md) for Gemini CLI sessions in the meantime.
