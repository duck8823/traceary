# Memory usefulness: synthetic evaluation

[日本語](usefulness-evaluation.ja.md)

## Decision

Issue #2447: **defer default retrieval changes**. Keep metadata-first staged retrieval and existing operational release gates. The small experiment demonstrates a runnable comparison, not a universal quality or cost advantage for memory. Do not add embeddings, background models, schema or release thresholds.

## Frozen protocol and split

[Corpus](../../experiments/memoryeval/corpus.json), [offline harness](../../experiments/memoryeval/evaluate.py), and [minimized measurements](evidence/memory-usefulness-2026-11-03.json) are versioned. The corpus is entirely synthetic. Decision-change is calibration; resume, cross-session evidence, workspace preference and superseded permission are four held-out task families. Expected answers/support fragments are excluded from model prompts. There is one sample per task/arm, no statistical significance or real-project generalization claim.

A preliminary no-history diagnostic exposed no comparative result. Before the final trial, a protocol defect was corrected: handoff does not expose event IDs, so every delivered snippet now has an explicit delivery evidence ID. This is **not authenticated native provenance**. The calibration family was not counted in primary results. No held-out responses were used to tune prompts/skills or select examples. The candidate/refinement summaries are operator-authored idealized fixtures; their generation/review cost is unknown, not zero.

Each task seeds a disposable store through the real CLI: history across sessions, a refinement, and an explicitly accepted synthetic memory. Every arm reads the same frozen store; no-history receives no context, staged uses body-free list metadata → bounded context → at most three selected details, and handoff-memory uses existing handoff plus at most two accepted memories. Reads are workspace-scoped; final setup time is the input cutoff and no subsequent writes occur. Complete synthetic inputs do not certify live host capture or handling of every missing-body case.

Context cap is 8192 bytes per arm and output instruction is 100 words. Budget overflow fails rather than silently truncating. Scoring validates corpus/prompt hashes, fixed question/options/budgets, delivered evidence refs, host/model/effort consistency and usage values. Correct choice and evidence coverage are separate: a correct unsupported choice is not success; absent model receipts are unmeasured, not losses.

## Measured results, 2026-11-03

Codex CLI 0.166.0, requested model `gpt-6.1-sol`, effort medium, ephemeral read-only/on-request. Shell, multi-agent, apps, web, configured MCP servers and dependency installation were disabled. Existing host hooks/Guardian/auth trust stayed enabled. Native JSONL does not expose the provider response model, so the actual resolved model is not independently attested. All 15 final responses completed under identical requested conditions; the table excludes three calibration responses.

| Arm | Correct | Evidence coverage | Retrieval calls | Context bytes | Input / output tokens |
|---|---|---|---|---|---|
| no-history | 0/4 | 0/4 | 0 | 0 | 77577 / 92 |
| staged | 4/4 | 4/4 | 16 | 7286 | 81674 / 161 |
| handoff-memory | 3/4 | 4/4 | 4 | 2292 | 76876 / 176 |

The cross-session handoff response chose UNKNOWN despite support fragments being present. Keep this negative outcome; do not rewrite it into a retrieval success. Staged and memory both avoided the tested stale-permission and cross-workspace errors. This does not prove reliable permission handling in arbitrary tasks.

Handoff used fewer calls/bytes but had one fewer correct primary response. Token counts include native system/config overhead; cache behavior, sequential execution order and one sample confound latency comparisons. End-to-end host latency, cached tokens, retrieval latency/bytes and setup/consolidation milliseconds are recorded separately in the measurements. No ranking by speed or universal percentage improvement follows. Corpus setup plus synthetic consolidation took 1311.302 ms; authoring/review, indexing/model-generation overhead beyond this fixture and priced API/subscription charges remain unknown. Pricing was not inferred from token counts.

Claude Code 2.1.316 was initially attempted: an unreviewed CLI argument was rejected locally before provider access; after removing only that argument, the guarded WIF route returned “Credit balance is too low” without a task answer. No credential was read/copied and no subscription route was substituted. A separately declared Codex trial supplied **all** measured arms; Claude/Codex responses are never pooled. Optional raw-history retrieval plus reranking remains unmeasured, not a losing arm.

## Reproduction and receipt contract

```sh
go build -o /private/tmp/traceary-eval .
python3 -B experiments/memoryeval/evaluate.py prepare --binary /private/tmp/traceary-eval --output /private/tmp/memory-inputs.json
python3 -B experiments/memoryeval/evaluate.py score --inputs /private/tmp/memory-inputs.json --output /private/tmp/memory-unmeasured.json
go test ./experiments/memoryeval
```

These commands are offline, use temporary HOME/store directories, and never call a model. To score an explicitly authorized trial, provide `--receipts FILE`: a JSON array with task, arm, prompt_sha256, host/version, model, effort, response `{answer,evidence}`, input_tokens, output_tokens, latency_ms and api_cost_usd (null when unknown). Optional cache/reasoning counts may be retained separately. Bind each response to the prepared prompt exactly. JSONL turn.completed usage supplies measured tokens; wall-clock duration supplies host latency. Record the native command/config/version and failures; do not hide auth errors with another provider.

The measured subject command was `codex exec --ephemeral --sandbox read-only --model gpt-6.1-sol --disable shell_tool --disable multi_agent --disable apps --disable skill_mcp_dependency_install -c 'model_reasoning_effort="medium"' -c 'approval_policy="on-request"' -c 'web_search="disabled"' -c 'mcp_servers.computer-use.enabled=false' -c 'mcp_servers.node_repl.enabled=false' --json --output-last-message ANSWER_FILE -`, with one frozen prompt on stdin per independent process. Run only under the host's existing authorization/trust policy. Recheck configured tool servers; do not weaken guards to reproduce. The harness does not execute this command automatically.

Raw synthetic responses/inputs and score files remain local under /private/tmp; the minimized report records their paths and hashes, not conversation bodies. A fresh prepare generates new IDs/timings, so its responses need new prompt hashes. Do not replay old receipts against new inputs.

## Follow-up boundary

No new retrieval feature is justified here. A later evaluation would need larger independently held-out coding tasks, randomized arm order/repeats, real generation/review overhead, missing-capture cases and declared pricing. Link completed operational evaluations rather than reopening them. Production source, memory and host configuration are unchanged; rollback removes this experiment/docs only.
