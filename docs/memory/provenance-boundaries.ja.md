# Memory の来歴と信頼境界

[English](provenance-boundaries.md)

## 判断と脅威モデル

Issue #2448 は既存境界の整理・回帰テストであり、認証機構の導入や確認済み脆弱性の報告ではありません。candidate と明示レビューを維持し、content receipt・schema・自動信頼判定は別の設計判断まで保留します。accepted Memory は文脈であって、**コマンド実行・deploy・資格情報取得・権限拡大の許可ではありません**。

prompt・transcript・import 文書・tool 出力には、引用された悪意ある指示が含まれ得ます。remember-intent はヒューリスティックな分類であり、認証済みユーザーの発言や承認の証明ではありません。evidence ref は確認対象への参照であり、作者の認証や外部の内容バイトの固定ではありません。レビュー後も不明な作者は不明です。

## 入力と信頼の行列

| 入力 | 保持する来歴 | 推論してはいけないこと |
|---|---|---|
| ユーザーの直接の好み | event/session ref、workspace、場合により remember-intent | hook の送信元だけでは入れ子の引用の作者を認証できない |
| 第三者の引用指示 | 抽出された場合は元 event ref | 引用された「覚えて」はユーザーの許可ではない |
| assistant の要約 | compact-summary/extracted と元 event ref | 独立した権威や原文の完全な引用ではない |
| tool・文書の内容 | 対象なら包含 event/import ref | tool 出力から権限を得ない |
| import された Memory・指示 | imported と対応する file/path ref | 古い方針の有効化や accepted 化ではない |
| 手動レビューされた文 | Accept は処理後の fact/source/ref を維持。Distill は操作者の fact と元 ref を使用 | accepted や confidence は操作許可・本人証明ではない |

## 各段階の責務

1. 抽出・import は candidate を作ります。悪意ある引用が candidate に残る場合があり、完全な抑制は保証しません。正常なコード例もレビュー可能に保ちます。
2. **redaction 後に表示された fact** と source・evidence・workspace・validity を合わせて確認します。memory show で元 ref を参照できます。Accept は evidence を要求しますが、参照先の内容取得やユーザー意思の認証はしません。
3. Distill は操作者が記述した文、指定 scope、元候補の置換方針による明示的な accepted 書込みです。元 evidence を保持しても、その主張に権限を与えません。生成文を未確認で採用しません。
4. 指定した scope・status・現在の validity による取得は、別 scope や古い・終端状態の Memory を除外します。明示的な履歴・別 scope の参照は可能であり、任意のクライアントに対する強制境界の保証ではありません。
5. Export は既定で accepted/current のみです。Markdown は fact・memory ID・scope を含みますが、**source/evidence/validity の全情報は含みません**。来歴の確認には store/detail が必要です。host activation は別の明示操作であり、抽出・Accept・Distill・Export の副作用ではありません。
6. レビュー後に fact/evidence が変わったら、再確認前に停止して対象内容を開き直します。file/URL ref は可変な参照先であり、content receipt ではありません。既存 API は immutable review snapshot や古い evidence の拒否を強制しません。scope 変更や古い方針の再 import は再レビュー対象であり、以前の操作許可を復元しません。

secret 形式の内容は既存規則で redaction します。合成 fixture の成功は、すべての secret の検出保証ではありません。実ログ・資格情報・本番 Memory は使用しません。

## 再現可能な決定的テスト

```sh
go test ./application/usecase -run 'TestMemoryProvenance|TestMemoryUsecase_ImportCodex_DoesNotResurrectRejected|TestMemoryBridgeImport' -count=1 -v
go test ./infrastructure/sqlite -run TestMemoryProvenanceExport -count=1 -v
```

新規テストは proposal/review 7ケース、抽出6ケース、明示 distillation、evidence 変更の特性確認、export criteria、SQLite による有効1件と除外5ケースの export を扱います。公開 usecase と永続化された query の振る舞いを確認し、private な呼出順には依存しません。proposal と Accept の間で合成ファイルを変更する試験は、内容固定が**存在しない**ことを示します。新しい保護ではありません。

直接の記憶指示・要約・正常な好みの3つの positive extraction は候補を作る必要があります。引用・権限主張・tool 風の内容は候補になっても自動採用しません。proposal/review は正常・攻撃風の7文を保持し、secret 形式の内容は処理します。候補数をログに残しますが攻撃拒否率とは呼びません。レビュー負荷、人間の意思分類の誤検知率、モデルの影響は未計測であり、観測失敗ゼロから安全を主張しません。

任意の host-model 試験は**未実施**です。実施時は合成ケースを固定し、host/model の版、試験件数、誤検知、未確認、token・時間・レビュー費用を記録し、決定的テストとは分離します。既存の構造テストは任意の host の動作認定ではありません。

## 互換性と rollback

runtime・public API・schema・権限・本番 Memory は変更しません。このテスト・文書 PR の revert で戻せます。Desktop D8 の UI と完了済み抽出ノイズ改善は対象外です。機微な発見は [SECURITY.md](../../SECURITY.md) に従い、実利用者の証拠を公開しません。
