# レポート集計契約

[English](./report-aggregate-contract.md)

## Structure-Behavior 設計メモ

### 要求の要約

- `report` は件数上限で切れた先頭部分を、期間全体の集計として表示してはいけません。
- DB 内部のページサイズと、呼び出し側が指定する結果上限は別の概念です。
- CLI と MCP は、同じ期間・フィルター・ページサイズ・結果上限に対して同じ集計スキーマを返します。
- レポート走査は本文を取得せず、観測範囲と切り詰めの発生元を返します。
- 既定は全件集計です。正の結果上限を指定した場合だけ、明示的な部分集計になります。

### 概念モデル

| 概念 | 状態 | 振る舞い | 不変条件 |
|---|---|---|---|
| レポート条件 | 期間、workspace、client、ページサイズ、結果上限 | 1 つの要求を検証して解決する | ページサイズは正、上限は 0（無制限）または正 |
| 取得範囲 | 本文を含まない session/event/command 行 | 全件または上限に確認用 1 件を加えた数まで読む | 全データ源で同じ実効期間とフィルターを使う |
| 結果の範囲情報 | 完全性、観測件数・期間、ページサイズ、上限、切り詰め理由 | 完全と部分を区別する | `partial` には `result_cap` の切り詰め証拠が必要 |
| レポートスナップショット | 集計値とデータ源別の範囲情報 | 分母が部分的な割合を出さない | CLI と MCP が同じ read model を直列化する |

### 責務の割り当て

| 責務 | 所有者 | 所有しない層 |
|---|---|---|
| 要求検証と既定 7 日間の解決 | application のレポート条件 | CLI/MCP adapter |
| 1 つの read snapshot、本文なし、上限付き走査 | SQLite レポート query adapter | usecase と presentation |
| 集計と完全な分母だけで割合を出す規則 | report usecase | SQL と output writer |
| flag/tool input、文言、テキスト表示 | CLI/MCP presentation | application core |

### 境界とインターフェース

| 境界 | 利用者 | 隠す詳細 | エラー契約 |
|---|---|---|---|
| `ReportQueryService.LoadReportWindow` | report usecase | SQL、ページング、確認用 1 件、transaction | 不正条件を拒否し、読み取り失敗を文脈付きで返す |
| `ReportUsecase.Generate` | CLI と MCP | データ源別集計と割合の出力可否 | 共通の report snapshot を返す |
| CLI `report` | operator/script | 互換用 `--limit` alias | `--limit` と `--page-size` の併用を拒否する |
| MCP `get_report` | agent host | application/infrastructure 型 | `page_size` と `result_cap` だけを使う |

### 振る舞いテスト

| 前提 | 操作 | 結果 | レベル |
|---|---|---|---|
| `result_cap` より多い行がある | レポート生成 | `partial`、観測範囲あり、割合なし | usecase/integration |
| 結果上限なし | レポート生成 | `complete`、割合あり | usecase/integration |
| CLI と MCP に同じ入力 | レポート直列化 | JSON を復号した値が一致 | presentation integration |
| 大きな保存本文がある | レポート集計 | SQL projection が本文列を選ばない | datasource/integration |
| `--limit` と `--page-size` を併用 | CLI 実行 | query 前に失敗 | CLI |

### TDD 計画

1. complete/partial の範囲情報と割合省略を確認する application の失敗テストを追加します。
2. 上限検出、観測範囲、本文なし走査を確認する SQLite の失敗テストを追加します。
3. CLI/MCP のスキーマ一致と flag 契約の失敗テストを追加します。
4. 条件・範囲情報の値オブジェクト、report query adapter、usecase の順に実装します。
5. CLI 内の集計を置き換え、完全集計時の互換フィールドを変えずに MCP tool を追加します。

### リスクとロールバック

- 手続き化リスク: CLI と MCP の双方に期間・上限・割合判断を複製すること。対策として application の条件と snapshot を 1 つにします。
- 早すぎる抽象化: 汎用レポート基盤は本 Issue の範囲を超えます。report window と範囲情報だけを共有します。
- 互換性: `period.from` / `period.to` と完全集計時の数値フィールドは維持します。旧 `--limit` はページサイズの非表示・非推奨 alias として残します。
- ロールバック条件: CLI/MCP のスキーマ差、部分集計での割合表示、集計時の本文取得。変更は追加的で migration はありません。
