# 再利用可能な教訓の合成比較評価

[English](lesson-evaluation.md)

## 判断

Issue #2449 は **本体への後続実装と自動昇格を保留**します。候補1件・task 5件では、生成・レビュー・保守費用を上回る held-out の利益を示せません。skill・新 CLI/schema・自律 optimizer・routing 変更は正当化できません。失敗・不確かな結果を保持し、[host 側レビュー票](lesson-review-template.ja.md) を明示レビューにだけ使います。

## 固定した証拠・候補・分割

[版管理する合成 corpus](../../experiments/lessoneval/corpus.json) は geometry request の失敗と成功を含み、request/generation の一致を独立した関数で確認できます。試行自身の成功宣言では実際の outcome を上書きしません。両試行を根拠に、非同期完了には request と generation の一致が必要で、同期的な純粋操作には適用しないという操作者記述の限定的候補を作りました。実ログから一般則を学習できたという主張ではありません。

候補は scope・適用条件・両試行の ref・失敗条件・反例・版・validity 開始を持ち、candidate のままです。実 store には書きません。1月の geometry を source、2月の network/queue/storage/pure-function/別 workspace を異なる held-out family にしました。corpus/candidate/prompt hash を試験前に固定し、正解や source の outcome label はモデルに渡しません。比較中に model size を変えません。

[offline harness](../../experiments/lessoneval/evaluate.py) が outcome・family/時刻分割・固定入力を検証し、no-lesson・現在の要約・候補を比較します。対の結果・不正な引用・source と同じ失敗・誤適用を記録します。全 arm は context 8192 bytes、出力100 words の同条件です。候補の引用だけで誤適用と判定しません。反例を引用して正しく適用を断るのは正常です。scope/同期操作の誤判断と候補引用が同時にある場合を proxy とし、内的推論や因果的影響は断定しません。

## 2026-11-03 の実測

Codex CLI 0.166.0、要求 model gpt-6.1-sol、effort medium、task/arm ごとの独立 process で15応答を取得しました。provider 応答の解決先 model は native 出力にないため、要求値として記録します。ephemeral/read-only/on-request、shell・multi-agent・apps・web・設定済み MCP・依存 install を無効化し、既存 Guardian・hook/auth trust は維持しました。入力は合成のみで、実履歴や許可情報は送りません。

| Arm | 判断の正答 | Protocol 全体の成功 | Schema 不一致 | 不正な引用 | Input / output tokens |
|---|---|---|---|---|---|
| no-lesson | 4/5 | 2/5 | 3 | 0 | 98191 / 325 |
| current-summary | 4/5 | 2/5 | 1 | 2 | 96976 / 408 |
| lesson-candidate | 5/5 | 2/5 | 0 | 3 | 97721 / 327 |

protocol 全体は全 arm が2/5でした。判断だけでは4/5、4/5、5/5で、差は一致する acknowledgement の1件です。no-lesson/current-summary は受領通知と完了通知を区別しました。候補が protocol の意味を補っているため、**追加仕様の提供**であり教訓の転用効果ではない可能性があります。古い/別 request、同期/別 workspace の判断は全 arm で正答でした。反復した source の失敗や誤適用 proxy は観測しませんでしたが、5件から安全性を主張しません。

モデルが evidence を配列でなく object にしたり、指定した task/candidate ref の代わりに説明や request ID を返したケースを保持します。scorer は不正形式を脱落・中断させず、実測した protocol 失敗に数えるよう修正し、判断のみの採点と分けました。正しい反例引用を誤適用と誤判定しない回帰テストも追加しました。固定 prompt・候補・モデル応答は変更していません。形式指示の曖昧さと少数・仕様依存の fixture が交絡するため、優位性を主張せず、同じ例で調整した後に再び held-out と呼びません。

[主担当が集計した最小化報告](evidence/lesson-usefulness-2026-11-03.json) に対の結果、corpus/candidate hash、token/cache、context bytes、host 遅延、local 入力/receipt/score の hash を記録します。時間は system/config overhead を含む単回の逐次観測です。model の順位や有意差は示しません。作成・生成・レビュー負荷、API 換算価格、subscription 請求額は不明でありゼロではありません。費用対効果の採用判断はできません。生の合成応答は local のみです。

## Offline 再実行

```sh
python3 -B experiments/lessoneval/evaluate.py prepare --output /private/tmp/lesson-inputs.json
python3 -B experiments/lessoneval/evaluate.py score --inputs /private/tmp/lesson-inputs.json --output /private/tmp/lesson-unmeasured.json
go test ./experiments/lessoneval
```

receipt がない応答は失敗でなく未計測です。許可した試験の --receipts FILE は task・arm・完全一致 prompt_sha256・host/version・model・effort・response `{answer,evidence}`・input/output token・latency・api_cost_usd を含みます。不明な料金は null です。native 条件・コマンドは別途提供する [Memory 評価](usefulness-evaluation.ja.md) と同じで、この corpus の独立 prompt を使います。上の offline コマンドは単独で使え、harness は provider 起動・install・生成教訓の実行をしません。

## レビュー・互換性・rollback

レビュー票は既存 fact/type/scope/evidence/artifact/validity に対応し、最終 distillation は操作者による明示操作のままです。過去の許可・資格情報を学習せず、private ログを export しません。棄却・失効候補の監査は既存 lifecycle を使います。Memory 自動採用・activation・skill install・host 指示書換え・model routing・権限変更は行いません。

利益が費用を上回る証拠がないため、本体への新規 follow-up は提案しません。将来に別の大きな評価を行うなら、明確な応答schemaと仕様依存の弱い独立 task を事前登録できますが、release blocker や自動最適化にはしません。rollback は実験・レビュー票・報告を除くだけで、本番データ・動作は変更しません。
