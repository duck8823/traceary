# Reusable lessons: synthetic contrast evaluation

[日本語](lesson-evaluation.ja.md)

## Decision

Issue #2449: **defer production follow-up and automatic promotion**. A one-candidate, five-task experiment does not demonstrate held-out benefit exceeding generation/review and maintenance costs. No skill, CLI/schema surface, autonomous optimizer or routing change is justified. Keep the negative/inconclusive findings below and use the [host-side review worksheet](lesson-review-template.md) only for explicit review.

## Frozen evidence, candidate and split

The [versioned synthetic corpus](../../experiments/lessoneval/corpus.json) contains a failed geometry request and a successful one, with independently executable request/generation equality checks. Claimed success in a source trajectory cannot override its observed outcome. Both source attempts are evidence for an operator-authored candidate: asynchronous completion requires the correct request and generation; synchronous pure operations are counterexamples. This is a narrowly scoped synthetic inference, not a proven general rule learned from real work logs.

The candidate records scope, prerequisites, both attempt refs, failure modes, counterexample, version and validity start. It remains `candidate`; nothing is written to a real Memory store. Geometry source tasks from January are disjoint from February network, queue, storage, pure-function and foreign-workspace task families. Freeze corpus/candidate/prompt hashes before trial. Expected answers and source outcome labels are not serialized into task prompts. Same-model comparisons never vary model size implicitly.

The offline [harness](../../experiments/lessoneval/evaluate.py) validates outcomes, family/time split and immutable inputs; compares no-lesson, a current summary, and the candidate; and records paired outcomes, unsupported citations, repeated source mistakes and incorrect boundary use. All arms have an 8192-byte context cap and 100-word output instruction. A candidate citation alone is **not** inappropriate use: citing its counterexample to correctly decline it is legitimate. The proxy flags a wrong scope/synchronous decision plus candidate citation; it cannot reveal hidden reasoning or causal influence.

## Measured trial, 2026-11-03

Codex CLI 0.166.0, requested model gpt-6.1-sol, effort medium, one independent process per task/arm, 15 completed responses. The native response does not expose the resolved provider model; the requested model is recorded rather than attested. Read-only/on-request, ephemeral execution, shell/multi-agent/apps/web/configured MCP/dependency-install disabled; existing Guardian and hook/auth trust preserved. Only synthetic prompts were supplied, no private traces or permissions.

| Arm | Decision correct | Full protocol correct | Invalid schema | Unsupported citations | Input / output tokens |
|---|---|---|---|---|---|
| no-lesson | 4/5 | 2/5 | 3 | 0 | 98191 / 325 |
| current-summary | 4/5 | 2/5 | 1 | 2 | 96976 / 408 |
| lesson-candidate | 5/5 | 2/5 | 0 | 3 | 97721 / 327 |

Full protocol success is 2/5 in every arm. Decision-only outcomes are 4/5, 4/5 and 5/5; the single difference is the matching acknowledgement case. The no-lesson/current-summary respondents interpreted an acknowledgement as receipt rather than completion. The lesson supplies that protocol convention, so this result may reflect **additional specification**, not transferable learning. All arms gave the correct stale/foreign-request and pure/foreign-workspace decisions. No tested repeated source mistake or inappropriate-use proxy fired; five cases do not establish safety.

Several model evidence fields were objects rather than lists or contained explanation/request IDs instead of the requested task/candidate refs. Preserve those failures. The scorer was hardened to count malformed model responses as measured protocol failures rather than aborting/dropping them; decision-only scoring remains separate. An additional regression prevents falsely labeling a correct counterexample citation as misuse. These scoring corrections did not alter frozen prompts, candidates or model responses. Evidence-format ambiguity and a tiny, specification-heavy fixture confound any benefit claim; do not retune on these cases and call them held out again.

The [minimized main-session aggregate report](evidence/lesson-usefulness-2026-11-03.json) records paired results, exact corpus/candidate hashes, token/cache counts, context bytes, host latency and local input/receipt/score digests. Latency is a single sequential observation including host/system/config overhead; do not rank models or infer significance. Authoring/generation/reviewer effort, API-equivalent price and subscription billing are unknown, not zero. No cost-benefit adoption claim is possible. Raw synthetic responses stay local, not in Git.

## Offline reproduction

```sh
python3 -B experiments/lessoneval/evaluate.py prepare --output /private/tmp/lesson-inputs.json
python3 -B experiments/lessoneval/evaluate.py score --inputs /private/tmp/lesson-inputs.json --output /private/tmp/lesson-unmeasured.json
go test ./experiments/lessoneval
```

Without explicit receipts every missing response is unmeasured, not a failure. For an authorized host trial, supply `--receipts FILE` with task, arm, exact prompt_sha256, host/version, model, effort, response `{answer,evidence}`, measured input/output tokens and latency, api_cost_usd (null if unknown). The native subject command and restrictions are the same as the [memory evaluation protocol](usefulness-evaluation.md#reproduction-and-receipt-contract); use this corpus's independently prepared prompts. That companion document is delivered separately; the offline commands above stand alone. The harness never launches a provider, installs code or executes generated lessons.

## Review, compatibility and rollback

The worksheet maps onto existing fact/type/scope/evidence/artifact/validity fields. Final distillation remains operator-authored and explicit; do not infer approvals/credentials from traces or export private logs. Rejected or stale candidates remain auditable under existing lifecycle operations. No auto-accepted memory, activation, skill install, host instruction rewrite, model routing or permission change.

No new production follow-up is proposed: the held-out evidence does not establish benefits exceeding costs. A separate larger evaluation could preregister a clearer response schema and less specification-dependent independent tasks, but that is not a release blocker or an automatic next optimization. Rollback removes this offline experiment/template/report, without changing production data or behavior.
