# Codex native adapter: 0.166.0 qualification work

[日本語](codex-0.166.0.ja.md)

**Approved delivery split (2026-11-05):** Safe [implementation/evidence #2474](https://github.com/duck8823/traceary/issues/2474) may integrate separately from [unfinished native qualification #2495](https://github.com/duck8823/traceary/issues/2495). Implementation merge is not native binding/state/recovery certification; unqualified functionality stays fail-closed/unknown.

**Status: optional decoder implemented; ordinary native TUI completed-context resume passed. Command-hook / managed-PTY recovery qualification remains incomplete.** This page tracks [#2474](https://github.com/duck8823/traceary/issues/2474), a child of native qualification #2468. Safe runtime recovery remains #2478.

## Implemented optional correlation

The normal composition roots inject an application CodexObservationClaimDecoder backed by a pure filesystem adapter. It borrows the already-acquired hook payload; it never reads stdin again, native histories, credential caches or transcript paths. No new handler, queue, worker, persisted schema, auth/approval configuration or wrapper is installed.

Format selection accepts literal **codex-cli 0.166.0** only. Bare 0.166.0, changed case, other patch versions and absent/nil decoders are unavailable: optional native IDs are omitted, while normal metadata remains unbound/admission_only. Selecting a format is NOT proof of installed source/version, generation, freshness or native readiness.

Candidate command-hook fields are canonical session_id and optional turn_id. The native event name must match the expected invocation boundary. Root identity keys are decoded literally, with size-first 1MiB payload, 128 top-level key, complete UTF-8 and 64-level subtree bounds. Duplicate/conflicting identity fields, case-fold aliases, nonstrings/null, invalid lexical values, malformed/trailing JSON and oversize input fail optional claims closed. Unknown bodies are skipped, never retained in the result. A thread_id-only exec-wire envelope is not a command-hook payload.

The result contains only SessionID, TurnID and decode Available. It cannot grant live binding, NativeProjectionEvidence, process control, approval, current working/waiting or recovery authority. EventID remains the original spool receipt; host event claims do not replace it. Other host paths and primary capture/stdout/exit/spool ordering remain unchanged. The original primary helper intentionally retains last-wins JSON handling; a duplicate session_id can therefore retain normal primary capture while the stricter optional decoder omits all native IDs.

## Observed metadata, not behavioral proof

The inspected installed binary reports codex-cli 0.166.0; package target is aarch64-apple-darwin, binary SHA256 39acf979729dd290f9a24ee38b2914e036c91b65952872e28ae2a6b88a7db7c1. Its offline app-server schema generator produced protocol schemas. This pins the inspected local artifact, not an upstream commit or compatible version range. Current [official hook documentation](https://learn.chatgpt.com/docs/hooks) and historical 0.145 fixtures are supporting format references, not proof of 0.166 native behavior.

Existing cached ChatGPT subscription authentication was confirmed by the CLI itself, without reading/copying/displaying credentials. A minimal standard path/locale/CA-path child environment passed metadata preflight; no inherited credential environment is forwarded. The startup-config diagnostic reports requirements, not effective exec approval policy. Product launchEnvironment is unchanged.

An isolated normal-CLI app-server stdio probe observed the effective native cwd, gpt-6.1-sol/openai, on-request approval, auto_review reviewer and readOnly sandbox before model requests. Using the CLI-owned cached ChatGPT subscription and a minimal nonsecret environment, one synthetic marker-only turn completed; the actual owner exited normally. A later explicit thread/resume restored that exact native UUID with the same target/model/provider/policy, and a fresh question recovered the original high-entropy marker (verified against the original input SHA256). The resumed actual owner also exited normally; no tool or approval request occurred. Neither the original input nor a pending input was replayed, and no new-conversation fallback was used.

The first resume harness stopped before its verification turn because it incorrectly required account/updated to be emitted on every process. This was an observation-availability failure, not a provider authentication failure. The corrected check used account/read with refreshToken=false, retained only account type chatgpt and discarded account details. No token or credential was requested or retained. This evidence qualifies only the isolated app-server API context-resume contract, **not** command hooks, exec/TUI resume, managed-PTY generation binding or adoption of a production app-server broker.

## Ordinary TUI completed-context measurement

A later isolated test used the ordinary CLI TUI with the standard `--no-daemon` option, not an adopted app-server execution profile. Current [official CLI documentation](https://learn.chatgpt.com/docs/cli/reference) documents this option as avoiding the shared background server; actual installed help also exposes it. The fixture requested a read-only sandbox without changing global authentication or approval settings. Native CLI-owned cached ChatGPT authentication was checked under the same minimal nonsecret environment; no API credential environment was forwarded.

A synthetic first turn recorded the marker and replied READY. After the exact actual owner was stopped and Wait returned -15, an explicit UUID resume in the original canonical cwd/capture store answered a **new** context question with the exact original high-entropy marker. Primary counts were session_started1, prompt2 and transcript2; both actual owned cleanup returns were -15, not normal-exit success. No old prompt or pending action was replayed in this completed-context case. [Body-free evidence](evidence/codex-0.166.0.json) preserves scope and failures.

The earlier no-prompt TUI metadata attempt observed no original SessionStart capture within its window, requested no model turn and reaped its actual owner -15. That FAIL is retained; no cause is inferred. Neither test establishes interrupted input/tool/approval behavior, actual native model/provider from terminal text, callback field semantics or managed generation binding. The user authorized a temporary metadata-only hook in this exact worktree. The four definitions and collector were placed without changing the original recorder or global settings. Two subsequent no-prompt ordinary TUI attempts produced zero metadata samples; both actual owners were reaped with -15. Adding an empty project config layer did not change that observation. The user also reported that these definitions are **not displayed** in `/hooks`. This is not evidence of a hook trust prompt being refused: discovery/load remains unconfirmed, and no native trust answer or bypass was sent. The redacted native doctor reported config.load OK but did not expose the temporary hook path; this does not establish a discovery cause.

## Additional native metadata measurements

Two actual native app-server processes were live concurrently in the same canonical cwd, without Traceary runtime hints. Separate thread/start calls returned distinct native IDs with matching model/provider/policy. No model turn was requested. This establishes same-cwd ambiguity, not a managed-generation witness.

For the exact completed synthetic conversation above, another metadata-only measurement observed owner A successfully resume that UUID while owner B, concurrently live, received native error code -32600 for the same resume. Both actual owners then exited normally. The rejection reason was not retained beyond its hash and is not proven to be a stable provider exclusion contract. Traceary must still own explicit session exclusion and prove old-owner inactivity; neither a UUID nor this one negative test replaces those checks. No model/input was replayed.

The first combined matrix stopped at a rejected hooks/list request after the distinct-ID case; its FAIL is retained. A second early harness omitted the successful-owner index on a rejected resume; that incomplete evidence is retained separately. The instrumented measurement records owner A success and owner B rejection, not two successful resumes. Hook inventory, command-hook payload, per-run composition and generic-PTY binding remain unqualified.

## Required native evidence before closing the leaf

| Requirement | Current evidence |
| --- | --- |
| Exact native hook payload and core capture/redaction/stdout/receipt equivalence | NOT_RUN on 0.166; synthetic decoder/Cobra tests only |
| Same-cwd concurrent runs, independent generation witness, resume ID reuse, late callbacks, missing hints, duplicate/reorder/expiry | Native API metadata: two simultaneous same-cwd owners produced distinct IDs; same-ID resume succeeded for A and was rejected for B. No runtime hints supplied. Command-hook/managed-generation, late/reorder/expiry matrix remains NOT_RUN; these IDs never bind on their own. |
| Working / waiting_input / waiting_approval / turn / process / unknown coverage | Unqualified; Stop is not process exit/readiness and approval telemetry is not permission |
| Global/per-run native hook composition | NOT_RUN; new hash/trust/settings require human review, never bypass/auto-trust |
| Same native conversation and original target/model/provider resume | PASS for isolated app-server stdio: exact UUID plus retained synthetic context, original cwd/model/provider/policy and actual-owner normal exits. Ordinary TUI completed-context resume also passed, with actual model/provider still unqualified; interrupted and managed-PTY recovery remain NOT_RUN. |

Traceary must reject changed target/source/unknown UUID before executing a resume plan; native CLI behavior is measured separately and is not assumed to provide these guards. No friendly-name/picker/last/fork/new-chat/model/API billing fallback is allowed. Native fixtures must precede #2478 service integration, not wait for it; the service owns durable records, actual old-owner inactivity, session exclusion and crash/duplicate semantics.

Production selected metadata still enters ObserveUnbound only. Current native interaction requires independently verified source/order/age and the SAME original #2464 ObservationStamp guarding both commit/read plus native discontinuity invalidation and host event-rate/ledger-limit qualification. Physical sleep/wake remains user-deferred NOT_RUN. Muse native work remains deferred; Gemini/Kimi are excluded. No UI/framework adoption or distribution is authorized by this leaf.

A later read-only native `doctor --json` query attempted to extract only project-layer/trust/managed-only metadata from its redacted report. It timed out after40 seconds. The direct diagnostic child was reaped by the subprocess timeout cleanup; its return code was not recorded. No model request, login, global configuration change or raw report retention occurred. This is a diagnostic availability failure, not evidence of authentication failure, project trust or managed-only policy. The query was not automatically retried.

## Exact-cwd read-only discovery diagnostic

A separate pinned native process performed only initialization, `hooks/list` for the dedicated worktree and `configRequirements/read`; it created no thread/turn or model request. The [official API documentation](https://learn.chatgpt.com/docs/app-server) describes these read operations; installed0.166.0 schemas were checked first. The exact cwd entry contained zero hooks from the temporary hook file, fourteen other hooks, no reported errors or warnings, and no requirements document. Unspecified managed-only policy is **not** proof of false/effective policy. Actual diagnostic Wait was0 and the three temporary files remained hash-identical. Only selected booleans/enums/counts were retained, not hook commands, raw responses, account values or credentials. This diagnostic is not adoption of an app-server execution profile.

A scoped read of non-secret user-config selectors found the main worktree explicitly trusted but no exact dedicated-worktree trust entry or root managed-only flag. That does not prove effective project trust or establish why hooks were absent. The user was asked here to review only a project-trust prompt for the dedicated folder if one appears. No project/global setting, native trust or authentication was changed, and no other AI received raw diagnostic/config data.

## Trusted project layer confirmed; missing hook discovery persists

On 2026-11-05 the maintainer confirmed that this dedicated folder is trusted and ordinary startup shows no trust prompt. A fresh pinned metadata-only native diagnostic then called initialize, config/read with includeLayers, hooks/list and configRequirements/read, without creating a thread/turn or model request. The exact test project layer was present and not disabled; the effective hooks feature was true. Nevertheless the temporary source still supplied zero hooks, with fourteen other hooks and no reported errors/warnings. Actual diagnostic Wait returned0 and all three temporary file hashes were unchanged.

An absent exact entry in the user-config projects table was not proof of effective distrust. Project trust is no longer a requested human action for this failure. The remaining issue is version-specific hook discovery; its cause is NOT_PROVEN. Do not bypass hook trust, edit global settings, adopt an execution broker or claim native hook/binding qualification. Only layer kinds, exact-target booleans, disabled flags and hook counts/enums were retained; no configuration bodies, hook commands, credentials or account values were saved.
