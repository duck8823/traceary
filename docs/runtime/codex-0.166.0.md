# Codex native adapter: 0.166.0 qualification work

[日本語](codex-0.166.0.ja.md)

**Status: implementation in progress; isolated native API context-resume passed, but command-hook / managed-PTY qualification is incomplete. Do not treat the decoder, schemas or synthetic tests as native certification.** This page tracks [#2474](https://github.com/duck8823/traceary/issues/2474), a child of native qualification #2468. Safe runtime recovery remains #2478.

## Implemented optional correlation

The normal composition roots inject an application CodexObservationClaimDecoder backed by a pure filesystem adapter. It borrows the already-acquired hook payload; it never reads stdin again, native histories, credential caches or transcript paths. No new handler, queue, worker, persisted schema, auth/approval configuration or wrapper is installed.

Format selection accepts literal **codex-cli 0.166.0** only. Bare 0.166.0, changed case, other patch versions and absent/nil decoders are unavailable: optional native IDs are omitted, while normal metadata remains unbound/admission_only. Selecting a format is NOT proof of installed source/version, generation, freshness or native readiness.

Candidate command-hook fields are canonical session_id and optional turn_id. The native event name must match the expected invocation boundary. Root identity keys are decoded literally, with size-first 1MiB payload, 128 top-level key and 64-level subtree bounds. Duplicate/conflicting identity fields, case-fold aliases, nonstrings/null, invalid lexical values, malformed/trailing JSON and oversize input fail optional claims closed. Unknown bodies are skipped, never retained in the result. A thread_id-only exec-wire envelope is not a command-hook payload.

The result contains only SessionID, TurnID and decode Available. It cannot grant live binding, NativeProjectionEvidence, process control, approval, current working/waiting or recovery authority. EventID remains the original spool receipt; host event claims do not replace it. Other host paths and primary capture/stdout/exit/spool ordering remain unchanged. The original primary helper intentionally retains last-wins JSON handling; a duplicate session_id can therefore retain normal primary capture while the stricter optional decoder omits all native IDs.

## Observed metadata, not behavioral proof

The inspected installed binary reports codex-cli 0.166.0; package target is aarch64-apple-darwin, binary SHA256 39acf979729dd290f9a24ee38b2914e036c91b65952872e28ae2a6b88a7db7c1. Its offline app-server schema generator produced protocol schemas. This pins the inspected local artifact, not an upstream commit or compatible version range. Current [official hook documentation](https://learn.chatgpt.com/docs/hooks) and historical 0.145 fixtures are supporting format references, not proof of 0.166 native behavior.

Existing cached ChatGPT subscription authentication was confirmed by the CLI itself, without reading/copying/displaying credentials. A minimal standard path/locale/CA-path child environment passed metadata preflight; no inherited credential environment is forwarded. The startup-config diagnostic reports requirements, not effective exec approval policy. Product launchEnvironment is unchanged.

An isolated normal-CLI app-server stdio probe observed the effective native cwd, gpt-6.1-sol/openai, on-request approval, auto_review reviewer and readOnly sandbox before model requests. Using the CLI-owned cached ChatGPT subscription and a minimal nonsecret environment, one synthetic marker-only turn completed; the actual owner exited normally. A later explicit thread/resume restored that exact native UUID with the same target/model/provider/policy, and a fresh question recovered the original high-entropy marker (verified against the original input SHA256). The resumed actual owner also exited normally; no tool or approval request occurred. Neither the original input nor a pending input was replayed, and no new-conversation fallback was used.

The first resume harness stopped before its verification turn because it incorrectly required account/updated to be emitted on every process. This was an observation-availability failure, not a provider authentication failure. The corrected check used account/read with refreshToken=false, retained only account type chatgpt and discarded account details. No token or credential was requested or retained. This evidence qualifies only the isolated app-server API context-resume contract, **not** command hooks, exec/TUI resume, managed-PTY generation binding or adoption of a production app-server broker.

## Required native evidence before closing the leaf

| Requirement | Current evidence |
| --- | --- |
| Exact native hook payload and core capture/redaction/stdout/receipt equivalence | NOT_RUN on 0.166; synthetic decoder/Cobra tests only |
| Same-cwd concurrent runs, independent generation witness, resume ID reuse, late callbacks, missing hints, duplicate/reorder/expiry | NOT_RUN; descriptor/env/cwd/native IDs alone never bind |
| Working / waiting_input / waiting_approval / turn / process / unknown coverage | Unqualified; Stop is not process exit/readiness and approval telemetry is not permission |
| Global/per-run native hook composition | NOT_RUN; new hash/trust/settings require human review, never bypass/auto-trust |
| Same native conversation and original target/model/provider resume | PASS for isolated app-server stdio: exact UUID plus retained synthetic context, original cwd/model/provider/policy and actual-owner normal exits. Exec/TUI and managed-PTY recovery remain NOT_RUN. |

Traceary must reject changed target/source/unknown UUID before executing a resume plan; native CLI behavior is measured separately and is not assumed to provide these guards. No friendly-name/picker/last/fork/new-chat/model/API billing fallback is allowed. Native fixtures must precede #2478 service integration, not wait for it; the service owns durable records, actual old-owner inactivity, session exclusion and crash/duplicate semantics.

Production selected metadata still enters ObserveUnbound only. Current native interaction requires independently verified source/order/age and the SAME original #2464 ObservationStamp guarding both commit/read plus native discontinuity invalidation and host event-rate/ledger-limit qualification. Physical sleep/wake remains user-deferred NOT_RUN. Muse native work remains deferred; Gemini/Kimi are excluded. No UI/framework adoption or distribution is authorized by this leaf.
