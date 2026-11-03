# Memory 有用性の合成評価

[English](usefulness-evaluation.md)

## 判断

Issue #2447 は **retrieval 既定動作の変更を保留**します。metadata-first の段階的取得と既存 release gate を維持します。この実験は比較手順の実行可能性を示すもので、Memory の一般的な品質・費用の優位性は示しません。embeddings・背景モデル・schema・新しい release 閾値を追加しません。

## 固定した条件と分割

[corpus](../../experiments/memoryeval/corpus.json)、[offline harness](../../experiments/memoryeval/evaluate.py)、[最小化した計測](evidence/memory-usefulness-2026-11-03.json) を版管理します。全入力は合成です。decision-change を校正に使い、再開・session 間の証拠・workspace の好み・失効した許可の4 task family を held-out にしました。正解・採点用断片はモデルに渡しません。各 task/arm は1試行で、統計的有意差や実プロジェクトへの一般化は主張しません。

予備の no-history 診断では arm 間の比較をしていません。最終試験前に、handoff が event ID を返さないという protocol の欠陥を修正し、配送した断片に明示的な evidence ID を付けました。これは **native な来歴の認証ではありません**。校正 family は主要結果から除外しました。held-out の応答による prompt/skill の調整や例の選別はしていません。要約・Memory は操作者が記述した理想化した fixture で、生成・レビュー費用はゼロではなく不明です。

実 CLI で task ごとに使い捨て store を作り、複数 session の履歴・refinement・明示的に accepted にした合成 Memory を記録します。全 arm が同じ固定済み store を使います。no-history は文脈なし、staged は本文なし metadata → bounded context → 最大3件の detail、handoff-memory は既存 handoff と最大2件の accepted Memory を使います。workspace を限定し、setup 完了時刻を cutoff として以降は書き込みません。合成入力の完全性は live capture やすべての本文欠落ケースの認定ではありません。

各 arm の context 上限は8192 bytes、出力指示は100 words です。超過時は黙って切り捨てず失敗します。scorer は corpus/prompt hash、固定した設問・選択肢・予算、配送済み ref、host/model/effort の一致、usage 値を確認します。正答と証拠 coverage は別で、根拠のない正解は成功に数えません。応答がなければ未計測であり敗北ではありません。

## 2026-11-03 の実測

Codex CLI 0.166.0、要求 model gpt-6.1-sol、effort medium、ephemeral/read-only/on-request です。shell・multi-agent・apps・web・設定済み MCP・依存自動導入を無効化し、既存 hook・Guardian・認証 trust は維持しました。native JSONL は provider 応答の model を返さないため、実際の解決先の独立した証明はありません。同じ要求条件で最終15応答を取得し、表は校正3応答を除外しています。

| Arm | 正答 | 証拠 coverage | 取得回数 | Context bytes | Input / output tokens |
|---|---|---|---|---|---|
| no-history | 0/4 | 0/4 | 0 | 0 | 77577 / 92 |
| staged | 4/4 | 4/4 | 16 | 7286 | 81674 / 161 |
| handoff-memory | 3/4 | 4/4 | 4 | 2292 | 76876 / 176 |

cross-session の handoff は必要断片が存在しても UNKNOWN と回答しました。この失敗を保持し、取得成功へ書き換えません。staged と Memory は今回の scope・古い許可のケースを通過しましたが、任意の作業で権限を正しく扱える保証ではありません。

handoff は取得回数・量を減らす一方、主要正答が1件少ない結果でした。token は native の system/config overhead を含み、cache・逐次実行順・単回試行が遅延の交絡要因です。host 全体遅延、cached token、取得遅延・量、setup/consolidation は計測ファイルで分離しました。速度ランキングや一般的な改善率は主張しません。fixture setup と合成 consolidation は 1311.302 ms でした。文の作成・レビュー、この fixture を超える indexing/モデル生成費用、API 換算価格・subscription の請求額は不明です。token から料金を推定していません。

Claude Code 2.1.316 の最初の試行は未登録引数で provider 前に拒否されました。その引数だけを除いた既存 WIF 経路も credit 不足で task 回答なしに終了しました。credential の取得・コピーや subscription 経路への切替はしていません。その後に別途宣言した Codex 試験で **全 arm** を測定し、異なる engine の結果は混合していません。任意の raw-history+reranking 比較は未計測であり、敗北と扱いません。

## 再実行と receipt

```sh
go build -o /private/tmp/traceary-eval .
python3 -B experiments/memoryeval/evaluate.py prepare --binary /private/tmp/traceary-eval --output /private/tmp/memory-inputs.json
python3 -B experiments/memoryeval/evaluate.py score --inputs /private/tmp/memory-inputs.json --output /private/tmp/memory-unmeasured.json
go test ./experiments/memoryeval
```

これらは offline で一時 HOME/store を使い、モデルを起動しません。明示許可した試験は --receipts FILE に task・arm・prompt_sha256・host/version・model・effort・response `{answer,evidence}`・input_tokens・output_tokens・latency_ms・api_cost_usd の配列を渡します。不明な費用は null です。cache/reasoning 件数は別に保持できます。prompt は完全一致で結び付け、JSONL turn.completed の usage と壁時計の時間を記録します。認証失敗を別 provider で隠しません。

実測した native コマンドは英語版の同節に全引数を記録しています。各独立 process に固定 prompt を stdin で渡し、既存の host 認可・trust に従って実行しました。再実行時も MCP の設定差を確認し、guard を弱めません。harness は native コマンドを自動実行しません。

合成応答・入力・採点ファイルの本文は /private/tmp に残し、版管理する報告には path/hash と最小化した結果だけを記録します。新しい prepare は ID・時間が変わるため、新しい hash の receipt が必要です。

## 後続作業と rollback

新しい retrieval 機能は正当化できません。将来の評価では、独立したより大きな coding task、実行順の無作為化・反復、実際の生成・レビュー費用、capture 欠落、価格条件が必要です。完了済み運用評価は再開せず参照します。本体 source・Memory・host 設定は変更せず、この実験・文書の削除だけで戻せます。
