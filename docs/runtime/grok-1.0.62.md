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

## Further local-only diagnostic: no qualification

A later attempt used an actual controlling PTY, the same pinned executable/restriction flags and a new empty packet-only cwd. It typed the documented local `/context` command but could not confirm its command-specific completion; **Enter was never sent**, and no model prompt was requested. This is FAIL, not effective memory/tool/backend proof. No terminal/account/context body was saved.

The diagnostic's normal SIGTERM cleanup did not yield actual Wait within its observation window. The original controller remains retained; it has not been restarted or treated as exited. Targeted `pgrep` omitted the child, while Apple's exact-parent `proc_listchildpids` returned the sole direct Grok child with matching UID and process group. Current argv/session metadata and cwd were unavailable. The maintainer explicitly waived those two current checks **for this diagnostic child only**; fixed driver/native source and the current direct parent/UID/group/Grok name were rechecked before sending SIGKILL once. **Signal delivery is not actual exit: original Wait remains pending.** No group, other AI, process adoption or product ownership/exit contract was changed. No further native attempt is allowed while this owner is unreconciled.

The harness itself also has a control-path defect: after its observation failure it stops reading the PTY and retains its Popen handle without a cleanup command channel. This is not evidence that the product daemon has that defect; its drain/control/Wait paths are separate. Before another attempt, keep PTY draining during cleanup, retain explicit control over the actual owned handle and persist Wait immediately, without broad scans or automatic force escalation. Whether stalled draining caused this instance remains unproven.

## Official source correspondence remains unverified

The [official CLI reference](https://docs.x.ai/build/cli/reference) documents session-only feature restrictions; the [release notes](https://x.ai/changelog/build) list1.0.62 on2026-10-16. The [official public source](https://github.com/xai-org/grok-build) is useful context, not automatically the installed source: lookup of the reported1474f9cbdd91 prefix returned HTTP422; the first100 tags contained no1.0.62 match; current SOURCE_REV differed, and its latest listed change was2026-09-29. This bounded lookup does not prove that matching source cannot exist. No public snapshot was certified as the installed binary, no binary/source was replaced and no model/privacy/backend guard was weakened.

## Official release-artifact byte identity

A separate read-only check followed the URL/platform/compression rules in the [official installer](https://x.ai/cli/install.sh), without executing it. The fixed [1.0.62 macOS ARM64 gzip artifact](https://x.ai/cli/grok-1.0.62-macos-aarch64.gz) was fetched without authentication under an80MiB download bound. Its71036654 compressed bytes have SHA256 `7a8ada02efca32c4fb065592db5739e497bf87937310f54297845d63e27773cb`. Stream expansion under a256MiB bound produced175577200 bytes with SHA256 `ec434e831afdcfce87780f2815fd732b1df8a4a8de96a491716ad75d13605a4e`, exactly matching a fresh read-only hash of the installed executable.

This establishes byte identity with that official release artifact, **not** full public-source revision correspondence, hook/model/provider/tool/backend qualification or safe recovery. The installer, downloaded binary and installed binary were not run; nothing was installed, replaced or made executable, no credential was read, and no existing process was signaled. The original diagnostic Wait remains pending and blocks further native attempts. The earlier source-lookup and native failures remain unchanged.
