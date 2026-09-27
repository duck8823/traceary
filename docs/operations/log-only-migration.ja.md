# ログ専用セッション移行の検証と復旧

[English](./log-only-migration.md)

実行対象は #2407 マージ後の `1eeea6d96f22cb6b9dc6d9cb382868877535fd1e` です。
採用はユーザーが承認しました。
待機中の Claude レビューは中止し、承認済みとは記録していません。
P1 #2404、テスト隔離 #2405、P2 #2406、P3 #2407 はマージ済みです。

## 実測した検証

実装 head `1bbf6151dea5842612f6b7e598002e309d667473` の全 unit・build・vet・lint、7 ホスト fixture、文書検証、fresh CI は合格しました。
マージ後の wave と、登録・receipt・nested capture・supervisor の選択した競合検査 `-race` count3 も合格しました。
隔離した合成データの store・HOME・hook state で、実 CLI の18回の操作が合格しました。
同一登録の既存 event 再利用、別 end の区別、境界後の追記、暗黙検索、旧 ended 記録の再利用、生成 STATUS の不在、supervisor 結果の保護を確認しました。
暗号化 export/import と再 import で、生の session 列と event 件数が復旧先に保持されました。
本番履歴は取得・変更していません。
CLI 証跡の digest は `44307bc3eb800fee392b173ea11e953f7638f7dc5836ac1946a5b574335708a9` です。
raw 出力はローカルに保持し、credential や暗号化入力は公開しません。
暗号化 portability bundle は全 store の backup ではありません。
実測した session_refinements は source1・復旧先0で、現在の bundle 形式は L2 refinement/coverage を export しません。
別途、新しい隔離 store への SQLite backup で、実際の refinement/coverage 行と event 件数の保持を確認しました。
それらを保持する復旧には full-store copy を使用し、今回 bundle schema は拡張しません。

## 実ホストの検証

| Host | Fixture/map | Native capture | 制限 |
| --- | --- | --- | --- |
| Claude 2.1.281 | PASS | PASS | 一時 plugin、tools/persistence なし、PATH と TRACEARY_BIN を固定 |
| Codex 0.157.1 | PASS | PASS | ephemeral・read-only |
| Antigravity 1.2.9 | PASS | PASS | plan の実 callback |
| Grok 1.0.41 stable | PASS | PASS | plan の1 turn |
| Muse 1.3.0-R3401.1 | PASS | PASS（echo） | frontend/hook の確認。Meta backend と数値 usage の認証ではない |
| Gemini 0.46.0 | PASS | NOT_RUN（完了） | server の UNSUPPORTED_CLIENT、hook 未到着。trust 拒否も観測し、迂回・更新なし |
| Kimi 0.39.1 | PASS | NOT_RUN（完了） | 契約側403。prompt/start は記録されたが応答未完了。認証 retry・更新なし |

version はインストール済みの観測値であり、最新 release を保証しません。
fixture/map は native 起動の成功と同一視しません。
最初の Claude probe は PATH 固定がなく、旧 writer が隔離行だけを終了扱いにしたため失敗です。
新しい隔離 store で PATH/TRACEARY_BIN の到達を確認し、ordinary ended_at が NULL のままであることを確認しました。
初期 harness の JSON key・合成時刻・SQLite UDF の誤りによる失敗も、成功として扱いません。
2026-09-28（JST）に、ユーザーが Gemini/Kimi の fixture のみでの縮退受入を明示承認しました。
この制限を残して P4 検証を受け入れ、7 ホストすべての完全な native 認証とは扱いません。
install・login・課金変更・global 設定変更・release 公開は行っていません。

## 復旧の境界

旧列・event・refinement・receipt の履歴は保持します。
旧 ended 行は追記・検索を拒否する理由ではなく、open 行もプロセス実行中の証拠ではありません。
one-shot の現在の書込みは supervisor が所有しますが、生の過去 metadata は actor 認証ではありません。
新しい隔離コピーに、実祖先を子より先に復旧してください。
backfill placeholder の label を認証とみなして one-shot 所有へ昇格させません。
万能な downgrade/roundtrip は保証しません。
旧 build の schema 拒否、optional spool receipt_id の破棄、旧 GC/doctor/reader の終了規則への逆戻りがあり得ます。
producer だけの部分 rollback や、本番データに対する旧 synthetic-close writer の実行は避けてください。
empty/nonempty の削除対象を拡張しません。
ordinary workspace の replay pinning は別の既知課題として残ります。
