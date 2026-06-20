# Antigravity migration status

[日本語](./antigravity.ja.md)

Antigravity is Google's successor to Gemini CLI as an AI agent host. This page describes what Traceary has discovered locally about Antigravity in v0.21.0 and what remains as follow-up work.

> **Summary:** No supported public CLI/hook contract for Antigravity has been confirmed in v0.21.0. Traceary hook and package implementation for Antigravity is tracked in #1195 (hook wiring) and #1196 (extension package). The existing [Gemini CLI extension](./gemini-extension.md) remains available for existing Gemini CLI installs.

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

## What is not confirmed in v0.21.0

- No public CLI binary or hook contract is confirmed. There is no `antigravity` command on PATH.
- No extension/plugin install mechanism equivalent to `gemini extensions install` has been confirmed.
- No hook event schema (session lifecycle, tool audit, prompt capture) has been established for Traceary.

## Follow-up

- **#1195** — Antigravity hook wiring (session, tool audit, prompt/transcript capture)
- **#1196** — Antigravity extension package for Traceary

Until these issues are resolved, Antigravity sessions will not appear in Traceary's event log. If you are migrating from Gemini CLI to Antigravity, continue using the [Gemini CLI extension](./gemini-extension.md) for Gemini CLI sessions in the meantime.
