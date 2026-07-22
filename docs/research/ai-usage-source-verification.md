# Decision: host usage sources and privacy boundaries (#1448)

[日本語](./ai-usage-source-verification.ja.md)

**Status:** source verification complete; implementation is split into the v0.32.0 adapter issues

**Date:** 2026-07-23

**Issue:** #1448

## Decision

Traceary will ingest only provider-reported usage from local, versioned host surfaces. It will not infer token counts from text, copy provider billing dashboards, intercept network traffic, or enable network telemetry.

The implementation baseline is:

| Host path | Classification | Authoritative usage boundary | Exact adapter scope |
|---|---|---|---|
| Codex `exec --json` | **available** | terminal `turn.completed.usage` | #1451: ingest the completed-turn counters once; `turn.failed` without counters is unavailable. |
| Codex interactive rollout | **available** | final cumulative `token_count` snapshot before `task_complete` or `turn_aborted` | #1451: calculate a monotonic cumulative delta within one rollout/turn segment; never sum snapshots. |
| Claude Code JSON/JSONL and local transcript | **available** | final `result.usage` for a one-shot run; unique assistant request (`requestId` + message id) for interactive provider calls | #1447: one-shot stream mode records only the terminal run summary; transcript mode records only unique calls. Never persist both accounting representations for one run. |
| Gemini CLI headless `stream-json` | **available** | terminal `result.stats` | #1455: ingest run/model totals from the terminal result. |
| Gemini CLI interactive hooks | **unavailable under the privacy boundary** | none | #1455: report unavailable. Do not install `AfterModel`, because its input also carries full model requests/responses. |
| Antigravity CLI status line | **partial** | an `idle` state snapshot is authoritative only as a conversation snapshot, not as a provider call | #1455: retain only cumulative total input/output as a non-additive session snapshot. Do not ingest `current_usage`, claim per-call identity, or estimate cost. Stop remains lifecycle-only. |
| Grok Build headless `streaming-json` | **available** | terminal `end` event keyed by `requestId`/`sessionId` | #1450: ingest `end.usage` once, including cache-read and reasoning fields. |
| Grok Build native hooks/TUI | **unavailable** | none | #1450: report unavailable; current hooks carry lifecycle and transcript paths but no usage. |
| Kimi Code local main-agent wire | **available** | `usage.record` with `usageScope=turn` | #1452: ingest each terminal record once from the main-agent wire and keep unsupported/error paths unavailable. |
| Kimi compact hooks | **not a usage source** | none | #1452: retain compact markers only; `token_count`/`estimated_token_count` describe context compaction, not provider usage. |

`partial` means that the documented counters are usable only at the stated granularity. It does not authorize Traceary to fill missing dimensions.

## Evidence baseline

The installed versions probed on 2026-07-23 were Codex CLI 0.145.0, Claude Code 2.1.212, Gemini CLI 0.46.0, Antigravity CLI 1.1.5, Grok Build 0.2.106, and Kimi Code 0.29.0. The probe prompt was the public, body-free instruction `Reply with exactly OK. Do not call tools.` (SHA-256 `ee8edbda12067ca9c4d226e355619c0cdb0dea01c475c52510e81cc9b678c7d3`). Raw outputs stayed under `/private/tmp`; only field names, event ordering, and numeric types were inspected.

### Codex

- The official SDK event type defines `turn.completed.usage` with input, cached input, cache-write input, output, and reasoning-output counters: https://github.com/openai/codex/blob/f343d1237d8d360e8224997a846acde0b04a17cd/sdk/typescript/src/events.ts
- The 0.145.0 live headless probe emitted one `thread.started` and one terminal `turn.completed` with those five numeric counters.
- A metadata-only inspection of the current local rollout found `token_count.info.total_token_usage` and `last_token_usage`, plus `turn_context.payload.turn_id`, `task_complete.payload.turn_id`, and `turn_aborted.payload.turn_id`. Across 5,541 observed cumulative snapshots, the total never decreased, including compaction boundaries. This is local evidence, not a promise that future versions cannot reset; an adapter must fail closed on regression.
- Interactive authority is the last cumulative snapshot in a turn segment. The turn usage is `terminal cumulative - baseline cumulative`. Intermediate `token_count` events are snapshots and must not be summed.

### Claude Code

- Claude Code documents `--output-format json|stream-json`; a stream ends with a final `result` statistics message: https://docs.anthropic.com/en/docs/claude-code/cli-usage
- Anthropic defines the billing usage dimensions, including input, cache creation, cache read, and output tokens: https://platform.claude.com/docs/en/api/go/messages
- The 2.1.212 live stream contained the same usage keys on assistant and final result messages. A metadata-only local transcript inspection found duplicate assistant rows with the same `requestId`, message id, and identical usage but different row UUIDs. Therefore UUID is not a provider-call identity; `requestId` plus message id is.
- Each unique assistant request is one provider response. In transcript mode, #1447 records those unique calls and creates no run-summary usage. In one-shot stream mode, it ignores assistant usage rows and records only terminal `result.usage` for the run. These modes are mutually exclusive accounting representations, so the same run cannot be counted twice. Aborted/error paths without the selected provider usage object are unavailable, not zero.

### Gemini CLI

- The official output contract defines terminal `result.stats`, including total/input/output/cached counters and per-model totals: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/packages/core/src/output/types.ts
- The official non-interactive runner emits that result from session metrics only at final success: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/packages/cli/src/nonInteractiveCliAgentSession.ts
- The current local live probe could not run because the installed individual Gemini client was rejected as unsupported. This does not change the official output classification, but #1455 must retain a versioned fixture before enabling the adapter.
- Gemini's `AfterModel` hook exposes `usageMetadata`, but it also receives the original LLM request and response/chunk: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/docs/hooks/reference.md
- Traceary will not install that hook for usage collection. Native interactive Gemini usage remains unavailable until a body-free terminal surface exists.

### Antigravity CLI

- Antigravity hooks document conversation/lifecycle fields and a Stop boundary, but no usage fields: https://antigravity.google/docs/hooks
- The status-line contract is body-free and documents conversation/model identity plus cumulative input/output and `current_usage` input/output/cache counters: https://antigravity.google/docs/cli/statusline
- Status-line payloads do not document a provider request id, Stop `executionNum`, or whether `current_usage` is additive to the cumulative totals. Traceary therefore stores only `total_input_tokens` and `total_output_tokens` from an `idle` payload as an immutable, non-additive session snapshot. A later snapshot supersedes it; aggregate queries select the latest snapshot per conversation and never add snapshot history. `current_usage`, cache fields, per-call usage, and price remain unavailable.
- A sandboxed 1.1.5 probe was stopped after its configured Traceary MCP server remained connecting; no completion claim was made from that attempt.

### Grok Build

- Grok documents the local headless `streaming-json` surface and terminal automation use: https://docs.x.ai/build/cli/headless-scripting
- The 0.2.106 live probe emitted a terminal `end` object with `requestId`, `sessionId`, `stopReason`, `num_turns`, and numeric input/cache-read/output/reasoning/total usage. The terminal event is sufficient without reading transcript bodies.
- Grok's hook contract and Traceary's versioned fixtures expose lifecycle, tool, compact, and transcript-path fields but no model or usage counters: https://docs.x.ai/build/features/hooks
- Direct xAI API usage shapes do not prove that Grok Build hooks expose the same fields. The native hook/TUI path remains unavailable rather than guessed from the provider API.

### Kimi Code

- Kimi's official hook contract establishes lifecycle/compact fields: https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html
- Traceary's [versioned host contract](../hooks/host-contract.json) records the sanitized live-probe method and the local main-agent wire side channel. The committed [v0.27.0 wire fixture](../../presentation/cli/testdata/kimi_hooks/v0.27.0/wire_main.jsonl) contains `usage.record` rows.
- The 0.29.0 live headless stdout contained no usage object. The live-observed local main-agent wire ended with a `usage.record` containing `model`, `usageScope=turn`, and numeric `inputOther`, `inputCacheRead`, `inputCacheCreation`, and `output` fields.
- Traceary may scan the wire line by line, decode only the event discriminator for non-usage rows, and fully decode only `usage.record`. It must never copy adjacent content/thinking/tool rows.
- `usage.record` has no documented provider call id. #1452 assigns the stable source identity `(host, session_id, agent_id, usage-record ordinal)`; the ordinal counts only `usage.record` rows in the append-only session wire, so copying/replaying the file elsewhere does not change the identity. A canonical payload fingerprint is stored as conflict evidence, not as the identity: the same identity and fingerprint is idempotent, while the same identity with different counters fails closed. Missing terminal records remain unavailable.

## Retry, stream, and failure rules

| Host | Retry/stream rule | Failure rule |
|---|---|---|
| Codex | Headless completed usage already aggregates the turn. Interactive uses one final cumulative delta per turn; intermediate snapshots are replacements. | Preserve an observed delta for an aborted turn; otherwise unavailable. |
| Claude | One-shot mode ignores assistant usage and records the terminal run summary; transcript mode records deduplicated request/message calls and no summary. Distinct request IDs remain distinct retry calls. | No usage object for the selected mode means unavailable. |
| Gemini | Only terminal result stats count; never add message chunks or `AfterModel` chunks. | Error without terminal stats is unavailable. |
| Antigravity | Persist immutable session snapshots, mark older ones superseded, and aggregate only the latest cumulative input/output snapshot. Ignore `current_usage`. | Missing/ambiguous idle snapshot is partial/unavailable. |
| Grok | Only one `end` per request ID counts; ignore incremental events. | Missing `end.usage` is unavailable. |
| Kimi | One `usage.record` is one reported turn record; replay is idempotent, not additive. | A retry/abort without its own record is unavailable. |

## Privacy boundary

Allowed persisted fields are host/provider/model identifiers, opaque session/call/run lineage, source version, availability, numeric usage counters, terminal reason, timestamps, and a versioned price-table identifier added later by #1456.

The following remain out of scope and must be discarded before the domain boundary:

- prompts, responses, thinking/reasoning text, compact summaries, and transcript content;
- tool names/arguments/results when reading usage sources;
- credentials, cookies, quota tokens, email/account identity, and raw host logs;
- network interception, provider billing API scraping, and default/opt-out telemetry;
- inferred token counts, inferred model names, and guessed prices.

Mixed JSONL readers must use bounded line scanning, decode a minimal event envelope first, fully decode only usage rows, redact paths from diagnostics, and never log a rejected row. Status-line readers must ignore account/email/quota fields.

## Relationship to the OTel decision

This decision does not reopen [the v0.26 OTel no-go](./otel-genai-export.md). Usage collection is local ingestion into Traceary's SQLite store. No OTLP exporter, network listener, default telemetry, or private payload span is added. A future exporter still requires a separate opt-in design after semantic-convention stability and a concrete consumer requirement.

## Exact follow-up scope

1. #1456: availability state, `call`/`run`/`session_snapshot` scope, authoritative identity, idempotent finalization, superseding snapshots, additive migration, and versioned price estimates.
2. #1453: run/parent/session/batch/ticket/PR/head/packet lineage without private bodies.
3. #1451: Codex headless terminal usage and interactive cumulative-delta adapter.
4. #1447: mutually exclusive Claude transcript-call or one-shot-run accounting, including request/message deduplication.
5. #1455: Gemini headless terminal stats, non-additive Antigravity cumulative conversation snapshots, and explicit interactive unavailability.
6. #1450: Grok headless `end` adapter and explicit native-hook unavailability.
7. #1452: Kimi main-wire `usage.record` adapter; compact counts remain excluded.
8. #1449: CLI/MCP aggregates over only finalized provider-neutral observations.
9. #1457: seven-day historical/live reconciliation, privacy inspection, and follow-up issue closure before release.
