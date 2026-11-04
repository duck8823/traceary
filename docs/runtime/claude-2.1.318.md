# Claude Code native qualification: 2.1.318

[日本語](claude-2.1.318.ja.md)

**Status: incomplete.** This tracks [#2475](https://github.com/duck8823/traceary/issues/2475), not completion of the native-host parent or runtime recovery service. [Body-free evidence](evidence/claude-2.1.318.json) distinguishes actual measurements, failures and unqualified states. No native-current or product recovery capability is enabled by these fixtures.

## Source and authentication

The installed normal CLI reports `2.1.318 (Claude Code)`. Native artifact SHA256 is `677a2ea7bf576cedd20e191d973914ed8a41796f53a63b1923381ddc4d5b5e7d`; normal entry SHA256 is `079234054a9e035839f50118b618c1568c8ae1e20950f9b668a60bf27722e3cc`. Both were checked before and after probes. No upstream commit or compatible range is inferred.

The normal foreground CLI owned its existing cached claude.ai / firstParty subscription authentication, confirmed under the same minimal nonsecret environment before requests. The orchestrator did not read/copy/display credential caches or forward credential values through the child environment; the native CLI alone used its host authentication. Print/SDK billing paths are not qualified here. Marker/input probes used plan mode, empty model tools, strict empty MCP and no-Chrome; the separate pending-tool fixture exposed only Bash behind a deny-only test guard; existing native hooks/Guardian remained active. No wrapper, login, installation, persistent global/project/auth/permission-setting modification or interactive approval answer was introduced.

## Actual completed-conversation resume

A synthetic marker-only turn completed with native model `claude-opus-5-5`. The first harness incorrectly assumed user-record content was always a list; this installed version used a string, so the observer missed the completed turn. The original FAIL remains recorded. After verifying the original-input hash, final marker, native turn completion and zero tools, the exact live fixture child was cleaned up with SIGTERM using fresh actual-parent/UID/command/cwd/session-marker checks. Exit 143 is **not** a normal-exit success or proof of a mid-turn interruption.

The corrected observer passed string/list and stale-prompt tests before use. Explicit foreground resume of the same own UUID in the original cwd and capture store recovered the original high-entropy marker from a new context question, with the same native model and subscription provider. The resumed actual owner exited 0. No original or pending input, approval or tool was replayed; no name/picker/continue/fork/background/new-chat/model/API fallback was used. This proves completed-conversation context resume only, not arbitrary interrupted-side-effect recovery or runtime exclusion/crash safety.

## Actual interrupted native conversation and explicit PTY input

A later foreground fixture accepted a synthetic user turn and was interrupted before any assistant record for that turn. The exact owned native child was reaped with exit 143. A subsequent normal foreground resume emitted native SessionStart with the same UUID/cwd and the expected fresh RUN_ID/generation/epoch hints. With `CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0`, an eight-second no-input startup window produced no UserPromptSubmit/Stop callback and no new user/assistant record. The [official environment reference](https://code.claude.com/docs/en/env-vars) documents disabling interrupted-turn auto-continuation with this variable; the foreground observations here, not the print-mode documentation, establish this fixture's behavior.

Two observer failures remain recorded as FAIL. The first sent a raw terminal burst plus Return but observed no UserPromptSubmit; it did not prove a native resume failure. A new explicit question used standard bracketed paste and a separately sent Return, recovered the original first-message marker with the original model/provider, and the actual owner exited 0. Its initial final-count assertion incorrectly counted three native local `/exit` presentation records as replay. A separate metadata verifier checked the actual immutable own record: the old pending input occurs once, the new explicit input once, and the only additional user-shaped rows are the exact local-command caveat/exit/stdout triplet. Correction tests reject arbitrary continuations and duplicate pending input; neither original FAIL was rewritten as PASS.

The resumed run emitted exactly one SessionStart, UserPromptSubmit, Stop and SessionEnd, all matching the original conversation/target and the new injected runtime hints. This is real ordinary-CLI interrupted-context/no-pending-input-replay evidence with **zero model tool calls**, not a pending-tool/approval fixture, a global exclusion proof, current-waiting qualification or certification of the production Runtime adapter. Raw native IDs, conversation bodies and account data were not shared.

## Actual pending-tool callback interruption

A separate ordinary-CLI plan-mode fixture requested only the read-only command `rtk proxy pwd`. A synchronous test-only PreToolUse guard recorded metadata, held that exact command, and would then return **deny**, never allow; every other command was denied immediately. The driver stopped the exact owned native AI while PreToolUse was pending and observed exit 143. No tool was executed or interactively approved, even if the driver had failed.

The first observer required the matching tool-use block to exist in the canonical transcript and failed. That FAIL is retained. A corrected new fixture measured transcript lag while holding the callback: even after five seconds, the tool-use block had not been persisted before SIGTERM. The actual owned native PreToolUse event, matching session/cwd/runtime hints/Bash command/plan mode, is evidence of the pending request; the missing transcript block is not treated as proof that nothing was pending. This is **not** a persisted-cutoff-tool fixture.

After actual old-owner exit, same-UUID foreground resume with auto-continuation disabled produced no new PreToolUse/PostToolUse/UserPromptSubmit/Stop callback, tool request or recorded native `api-request` during the eight-second no-input window. A new explicit tool-free context question recovered the original first-message marker with the same model/provider, no resumed tool callbacks, and actual exit 0. The old pending request and the new input each occur once. Permission-dialog recovery and production Runtime integration remain unqualified; the result is not current waiting/approval authority or an external/global exclusion proof.

## Native hook input and capture

A test-only additive metadata observer received one native callback each for SessionStart, UserPromptSubmit, Stop and SessionEnd. It read bounded stdin, emitted no stdout/decision, and retained only root field/type inventory, match booleans and hashes in an isolated store. It did not wrap the agent or replace/add a second primary recorder. The original Traceary transcript capture increased exactly once (4 to 5), and the actual native owner exited 0.

All four native payloads had canonical string session_id and cwd matching the own conversation/target. UserPromptSubmit, Stop and SessionEnd included string **prompt_id**, not turn_id; SessionStart had neither. Prompt/Stop permission_mode was plan; SessionStart/End omitted it. None carried run/generation/epoch fields, and no Traceary runtime hint names were supplied. These observations alone do not authenticate managed generation, freshness, current waiting or approval authority. prompt_id is prompt correlation, **not silently reinterpreted as native turn_id**. Native histories are not a current-state interface.

Current [official hook documentation](https://code.claude.com/docs/en/hooks) explains prompt_id as prompt correlation and notes that transcript writes can lag. Current docs are supporting semantics, not a substitute for this exact-version measurement. Stop is not process exit/readiness; SessionEnd is lifecycle telemetry, not authority to adopt or signal a process.

## Rejected composition candidate

Adding the **same existing** `traceary hook transcript claude` through additional per-run settings did not deduplicate it in this installed configuration. Native Stop handlers increased from 3 to 4, and one synthetic response produced **two** core transcript events (2 to 4), despite zero native hook errors. The actual owner then exited 0. This is a recorded FAIL: never ship this duplicate-primary-handler approach or infer that appending descriptor flags safely replaces a global handler. The later metadata-only observer preserved one primary capture; it does not solve primary-handler enrichment or generation binding.

## Actual concurrent foreground opening

A later metadata-only test opened the exact same completed synthetic UUID in two normal foreground processes. A was confirmed live with a matching native SessionStart before B was attempted; B also emitted SessionStart for that UUID, and both actual processes were live. No new user or model-request records appeared in the own conversation. Both actual fixture handles were then deliberately cleaned up with SIGTERM (143/143), not called normal exits.

This proves that foreground resume/session-start alone does **not** provide single-process ownership for this installed version. It does not test concurrent model turns, arbitrary descendant quiescence or a stable global lease. Traceary must not infer exclusion from one CLI startup, one history PID, or the fact that its own request is serialized. The corrected test collector checked the actual RUN_ID hint name; no generation or recovery authority is enabled.

## Remaining qualification and safety gates

| Requirement | Status |
| --- | --- |
| Exact source and four native hook field/type inventories | Measured above, including one actual pending PreToolUse; permission-dialog and successful PostToolUse events NOT_RUN |
| Same native conversation context/cwd/store/model/provider | PASS completed-context and controlled interrupted-context fixtures; two concurrent foreground starts observed. Pending PreToolUse callback covered; persisted-cutoff-tool/permission-dialog and production managed exclusion remain unqualified |
| Global/per-run primary-handler composition | Duplicate-primary candidate FAILED and rejected; safe descriptor enrichment unqualified |
| Same-cwd concurrency, independent generation, reused-ID/late/missing-hint/duplicate/reorder/expiry matrix | Incomplete; descriptor/env/cwd/native IDs alone remain unbound |
| working / waiting_input / waiting_approval / process | Not qualified; permission metadata is not a visible wait or permission decision |
| Product adapter, independent implementation review, final-head CI/security/wave | Pending; no issue closure or production capability inferred |

Safe runtime recovery remains #2478: durable verified identity, original source/target/profile, actual old-owner inactivity, session exclusion and crash/duplicate semantics. Exact UUID alone never authorizes execution. In particular, the installed CLI help says background resume can copy an already-running session; background/fork/copy paths are forbidden for recovery. The foreground-opening behavior was measured above; it is not a native global lease. After the maintainer’s explicit 2026-11-04 scope decision in [#2478](https://github.com/duck8823/traceary/issues/2478), recovery guarantees cover the old Traceary-managed AI process and Traceary-managed request exclusion only. Coordinating external native CLI processes or proving every descendant stopped is outside the guarantee. This native limitation must be disclosed, not worked around with an extra server or treated as a requirement to fence external sessions. Managed exclusion and verified old-managed-AI exit remain required in #2478.

Physical sleep/wake remains user-deferred NOT_RUN; no sleep, launchd or power-setting operation was performed. Muse native work remains deferred. No UI/framework adoption or distribution is included. Rollback keeps original capture/history/auth and disables only an optional host/version capability, returning to unknown/unbound.
