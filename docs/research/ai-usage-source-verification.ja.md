# 決定: host 利用量 source と privacy 境界 (#1448)

[English](./ai-usage-source-verification.md)

**Status:** source 検証完了。実装は v0.32.0 の adapter Issue に分割する

**Date:** 2026-07-23

**Issue:** #1448

## 決定

Traceary は、version を特定できるローカル host surface が返した利用量だけを取り込む。本文から token 数を推定せず、provider の請求 dashboard を複製せず、network traffic を傍受せず、network telemetry を有効化しない。

実装の基準は次のとおり。

| Host 経路 | 分類 | 利用量を確定する境界 | Adapter の正確な範囲 |
|---|---|---|---|
| Codex `exec --json` | **available** | 終端 `turn.completed.usage` | #1451: 完了 turn の counter を一度だけ取り込む。counter のない `turn.failed` は unavailable。 |
| Codex 対話 rollout | **available** | `task_complete` または `turn_aborted` 直前の最終 cumulative `token_count` snapshot | #1451: 同じ rollout/turn 区間の単調な cumulative 差分を計算する。snapshot を合算しない。 |
| Claude Code JSON/JSONL とローカル transcript | **available** | one-shot 全体は最終 `result.usage`、対話 provider call は一意な assistant request（`requestId` と message id） | #1447: 同じ provider request の重複 assistant 行を除外し、cache counter を保持する。 |
| Gemini CLI headless `stream-json` | **available** | 終端 `result.stats` | #1455: 終端 result の run/model 合計を取り込む。 |
| Gemini CLI 対話 hook | **privacy 境界上 unavailable** | なし | #1455: unavailable を返す。`AfterModel` は完全な request/response も渡すため導入しない。 |
| Antigravity CLI status line | **partial** | `idle` state snapshot は conversation snapshot としてのみ確定可能。provider call 単位ではない | #1455: privacy-safe な cumulative/current snapshot の claim だけを取り込む。call 単位の identity と cost は主張しない。Stop は lifecycle 専用。 |
| Grok Build headless `streaming-json` | **available** | `requestId`/`sessionId` を持つ終端 `end` event | #1450: cache-read と reasoning を含む `end.usage` を一度だけ取り込む。 |
| Grok Build native hook/TUI | **unavailable** | なし | #1450: unavailable を返す。現行 hook は lifecycle と transcript path を持つが利用量を持たない。 |
| Kimi Code ローカル main-agent wire | **available** | `usageScope=turn` の `usage.record` | #1452: main-agent wire の各終端 record を一度だけ取り込む。非対応/error 経路は unavailable のままにする。 |
| Kimi compact hook | **利用量 source ではない** | なし | #1452: compact marker だけを維持する。`token_count`/`estimated_token_count` は context 圧縮量であり provider 利用量ではない。 |

`partial` は、文書化された counter を記載した粒度でだけ利用できるという意味であり、不足項目を Traceary が埋めてよいという意味ではない。

## 証拠の基準

2026-07-23 に確認した導入済み version は Codex CLI 0.145.0、Claude Code 2.1.212、Gemini CLI 0.46.0、Antigravity CLI 1.1.5、Grok Build 0.2.106、Kimi Code 0.29.0。probe prompt は公開して問題のない `Reply with exactly OK. Do not call tools.`（SHA-256 `ee8edbda12067ca9c4d226e355619c0cdb0dea01c475c52510e81cc9b678c7d3`）とした。raw output は `/private/tmp` に残し、field 名、event 順序、数値型だけを調べた。

### Codex

- 公式 SDK event 型は、input、cached input、cache-write input、output、reasoning-output counter を持つ `turn.completed.usage` を定義している: https://github.com/openai/codex/blob/f343d1237d8d360e8224997a846acde0b04a17cd/sdk/typescript/src/events.ts
- 0.145.0 の headless probe は `thread.started` 1 件と、この 5 counter を持つ終端 `turn.completed` 1 件を出力した。
- 現在のローカル rollout を metadata だけに限定して確認した結果、`token_count.info.total_token_usage` / `last_token_usage`、`turn_context.payload.turn_id`、`task_complete.payload.turn_id`、`turn_aborted.payload.turn_id` が存在した。compaction を含む 5,541 snapshot で cumulative total の減少はなかった。ただし、これは将来 version でも reset しないという保証ではない。減少時は adapter が fail closed する。
- 対話経路では、turn 区間の最終 cumulative snapshot を確定値にする。turn 利用量は `終端 cumulative - 開始時 cumulative`。途中の `token_count` は snapshot なので合算しない。

### Claude Code

- Claude Code は `--output-format json|stream-json` を文書化し、stream は統計を持つ最終 `result` で終わる: https://docs.anthropic.com/en/docs/claude-code/cli-usage
- Anthropic は input、cache creation、cache read、output token を含む請求上の利用量項目を定義している: https://platform.claude.com/docs/en/api/go/messages
- 2.1.212 の live stream では assistant message と最終 result に同じ利用量 key が存在した。ローカル transcript を metadata だけに限定して確認すると、`requestId`、message id、利用量が同一で、行 UUID だけが異なる assistant 行が重複していた。したがって UUID は provider call identity に使わず、`requestId` と message id を使う。
- 一意な assistant request 1 件を provider response 1 件として扱う。one-shot 全体では最終 `result.usage` を正本にする。provider usage object がない abort/error は zero ではなく unavailable。

### Gemini CLI

- 公式 output contract は total/input/output/cached counter と model 別合計を含む終端 `result.stats` を定義している: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/packages/core/src/output/types.ts
- 公式 non-interactive runner は、最終成功時に session metrics から result を 1 回だけ出力する: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/packages/cli/src/nonInteractiveCliAgentSession.ts
- 現在のローカル probe は、導入済み individual Gemini client が非対応として拒否されたため実行できなかった。公式 output の分類は変えないが、#1455 では adapter を有効化する前に version 付き fixture を追加する。
- Gemini の `AfterModel` hook は `usageMetadata` を持つ一方、元の LLM request と response/chunk も受け取る: https://github.com/google-gemini/gemini-cli/blob/f743ab579098f982d87ea3f2472c2405f6999297/docs/hooks/reference.md
- Traceary は利用量取得のためにこの hook を導入しない。本文を含まない終端 surface が追加されるまで、native interactive Gemini は unavailable とする。

### Antigravity CLI

- Antigravity hook は conversation/lifecycle field と Stop 境界を文書化しているが、利用量 field はない: https://antigravity.google/docs/hooks
- status-line contract は本文を含まず、conversation/model identity、cumulative input/output、`current_usage` の input/output/cache counter を文書化している: https://antigravity.google/docs/cli/statusline
- status-line payload には provider request id も Stop の `executionNum` もない。Traceary は `idle` conversation snapshot を保存できるが、正確な call 単位の利用量や価格には変換しない。
- sandbox 内の 1.1.5 probe は、設定済み Traceary MCP server が接続中のままになったため停止した。この attempt から completion claim は作らない。

### Grok Build

- Grok はローカル headless `streaming-json` surface と automation 用途を文書化している: https://docs.x.ai/build/cli/headless-scripting
- 0.2.106 の live probe は、`requestId`、`sessionId`、`stopReason`、`num_turns` と、input/cache-read/output/reasoning/total の数値利用量を持つ終端 `end` object を出力した。transcript 本文を読まずに確定できる。
- Grok の hook contract と Traceary の version 付き fixture は lifecycle、tool、compact、transcript-path field を持つが、model と利用量 counter は持たない: https://docs.x.ai/build/features/hooks
- xAI API の利用量 shape は Grok Build hook が同じ field を持つ証拠にはならない。provider API から推測せず、native hook/TUI 経路は unavailable とする。

### Kimi Code

- Kimi 公式 hook contract と Traceary の v0.27.0 sanitized fixture は lifecycle/compact field を確定している: https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html
- 0.29.0 live headless stdout には利用量 object がなかった。一方、文書化されたローカル main-agent wire の末尾には、`model`、`usageScope=turn`、数値の `inputOther`、`inputCacheRead`、`inputCacheCreation`、`output` を持つ `usage.record` があった。
- Traceary は wire を一行ずつ走査し、利用量以外の行では event discriminator だけを decode し、`usage.record` だけを完全に decode する。隣接する content/thinking/tool 行を複製してはならない。
- `usage.record` には文書化された provider call id がない。#1452 は #1456 の provider-neutral idempotency（session/agent/source position と payload fingerprint）を使い、衝突する replay を検出し、終端 record がない場合は unavailable を維持する。

## Retry、stream、failure の規則

| Host | Retry/stream 規則 | Failure 規則 |
|---|---|---|
| Codex | Headless completed usage は turn 全体を集約済み。対話経路は turn ごとに最終 cumulative 差分を 1 件だけ使い、途中 snapshot は置換する。 | abort 前に差分を観測できた場合は保持し、なければ unavailable。 |
| Claude | provider request/message identity で stream/transcript 重複を除外する。異なる request ID は異なる retry call とする。 | 最終 usage object がなければ unavailable。 |
| Gemini | 終端 result stats だけを数える。message chunk や `AfterModel` chunk を加算しない。 | stats 付き終端 result がなければ unavailable。 |
| Antigravity | 最新 conversation snapshot を置換する。status-line snapshot を加算しない。 | `idle` snapshot がない、または対応が曖昧なら partial/unavailable。 |
| Grok | request ID ごとに `end` 1 件だけを数える。途中 event は無視する。 | `end.usage` がなければ unavailable。 |
| Kimi | `usage.record` 1 件を host が報告した turn record 1 件とする。replay は加算せず冪等にする。 | record を持たない retry/abort は unavailable。 |

## Privacy 境界

永続化してよいのは、host/provider/model identifier、opaque な session/call/run lineage、source version、availability、数値の利用量、terminal reason、timestamp、および後で #1456 が追加する version 付き price-table identifier。

次の情報は対象外とし、domain 境界より前に破棄する。

- prompt、response、thinking/reasoning 本文、compact summary、transcript content。
- 利用量 source を読む際の tool 名、argument、result。
- credential、cookie、quota token、email/account identity、raw host log。
- network interception、provider billing API scraping、既定または opt-out の telemetry。
- 推定 token 数、推定 model 名、推測価格。

混在 JSONL reader は bounded line scan を使い、最初に最小 event envelope だけを decode し、利用量行だけを完全 decode する。診断では path を伏せ、reject した行を log に出さない。status-line reader は account/email/quota field を無視する。

## OTel 決定との関係

この決定は [v0.26 の OTel no-go](./otel-genai-export.ja.md) を再検討するものではない。利用量収集は Traceary の SQLite store へのローカル取り込みである。OTLP exporter、network listener、既定 telemetry、private payload span は追加しない。将来 exporter を追加する場合も、semantic convention の安定と具体的 consumer 要件を満たした後、別の opt-in 設計が必要。

## Follow-up の正確な範囲

1. #1456: availability state、authoritative call identity、冪等な確定、additive migration、version 付き価格推定。
2. #1453: private 本文を含まない run/parent/session/batch/ticket/PR/head/packet lineage。
3. #1451: Codex headless 終端利用量と対話 cumulative-delta adapter。
4. #1447: Claude request/message 重複除外と one-shot result 集約。
5. #1455: Gemini headless 終端 stats、Antigravity conversation snapshot、対話経路の明示的 unavailable。
6. #1450: Grok headless `end` adapter と native-hook unavailable の明示。
7. #1452: Kimi main-wire `usage.record` adapter。compact count は除外。
8. #1449: 確定済み provider-neutral observation だけを使う CLI/MCP aggregate。
9. #1457: 7 日分の履歴/live reconciliation、privacy 検査、follow-up Issue 完了後に release。
