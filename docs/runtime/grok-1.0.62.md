# Grok Build native qualification: 1.0.62

[日本語](grok-1.0.62.ja.md)

**Status: discovery only. No native AI turn, callback or safe recovery is qualified.** This tracks [#2477](https://github.com/duck8823/traceary/issues/2477), not parent completion. [Body-free metadata](evidence/grok-1.0.62.json) separates actual observations from unrun gates.

## Measured source

The installed direct arm64 executable reports `grok 1.0.62 (1474f9cbdd91)`, SHA256 `ec434e831afdcfce87780f2815fd732b1df8a4a8de96a491716ad75d13605a4e`. The displayed build prefix is not a proven full upstream revision. No compatible range is inferred.

Actual help distinguishes new explicit UUID sessions from resume/fork/continue/code restoration and exposes ordinary tool/permission/subagent/web options. Root help does not display no-memory/no-leader, but each option is accepted in a help-only parse while a deliberately invalid option fails. This is parser availability, not proof of effective privacy, standalone ownership or tool safety. The source-only discovery executed no leader/memory/auth/configuration management or model command; the separate startup attempt is described below.

## Startup without a model prompt

A normal TUI startup was attempted from an empty packet-only fixture cwd, with explicit no-memory/no-subagents/no-leader, empty builtin tools, disabled web and plan mode. No model prompt or approval/trust/login response was sent, no source/private workspace was exposed, and raw terminal was not recorded. No original SessionStart capture was observed. The actual owned process was stopped and reaped0; this is cleanup, not model success, and does not prove backend quiescence or a cause of missing capture. Effective memory/tools/backend ownership remain unqualified. A separate read-only inspect retained only known-section booleans/counts, never raw config, paths, server names or authentication values.

## Contract to verify

Current [official hook documentation](https://docs.x.ai/build/features/hooks) describes camelCase identity/target/tool fields and hook environment hints. It also documents that many hook failures do not block tool execution. Treat that as a warning against using a failed observer as a safety gate, not as installed-version native qualification. Project-hook trust must not be bypassed.

Grok remains a restricted engine: use only scoped synthetic/nonsecret input, no private history or credential collection, no implicit API/engine fallback, and no source-workspace access before native tool/data boundaries are proven. Option recognition cannot certify memory exclusion, native model/provider, callback correlation or whether the TUI owns its AI backend. A different native headless/stdio/leader mode must not silently replace the ordinary CLI contract.

Before product code, require actual native inventory and an independent Structure-Behavior checkpoint. Verify original primary capture/stdout/receipts, exact target/conversation/model/provider, managed old-owner Wait, one explicit managed resume and no pending input/tool/approval replay. Unsupported live states need a verified reason; unrun tests stay unrun. Then affected/reverse tests, independent final review, fresh CI/security and same-SHA wave. No UI, distribution, wrapper/broker or production operation. Muse and physical sleep/wake remain separately deferred.
