# Durable memory ガイド

[English](./README.md)

この文書では、Traceary の durable memory 層が全体のどこに位置づくのかと、memory 関連の CLI / MCP 操作がどうつながるのかを整理します。

## durable memory の位置づけ

Traceary は、エージェントの文脈を 3 層で扱います。

| 層 | 役割 | 代表例 |
| --- | --- | --- |
| Audit / Archive | 検索・監査・後追い確認のための元データ | 生の event、session 境界、command audit |
| Working memory | 再開や handoff のためにその場で組み立てる文脈 | handoff pack、直近コマンド、compact summary |
| Durable memory | セッションをまたいで残したい事実 | decision、constraint、preference、lesson、artifact ref |

Durable memory は意図的に小さく保ちます。会話全文やログ全文の置き場ではなく、監査ログの代替でもありません。

## memory のライフサイクル

Durable memory には、type・scope・status・confidence・evidence ref・任意の artifact ref があります。

### status

- `candidate`: 抽出または提案されたが、まだレビュー前の memory
- `accepted`: セッションをまたいで再利用したい active memory
- `rejected`: 再利用しないと決めた candidate
- `superseded`: 新しい accepted memory に置き換えられた古い memory
- `expired`: 期限付きで有効だったが、いまは active ではない memory

既定の active-memory 系の取得では、active な accepted memory だけを対象にします。

## evidence ref と artifact ref の違い

Traceary は、memory に 2 種類の参照を持たせます。

- **evidence ref**: その fact を正当化する根拠
  - 例: `event:...`, `session:...`, `issue:#462`, `pr:#468`
- **artifact ref**: 次に人やエージェントが開きたくなる対象
  - 例: `file:docs/release/README.md`, `url:https://...`, `command:go test ./...`

accepted memory には evidence ref が必須です。artifact ref は任意です。

## memory コマンドの関係

### 手動で書き込む経路

人やエージェントが、残すべき事実を明示的に記録したいときは次を使います。

- `traceary memory remember`
- `traceary memory propose`
- `traceary memory accept`
- `traceary memory reject`
- `traceary memory supersede`
- `traceary memory expire`

### 抽出する経路

既存の session signal から candidate memory を起こしたいときは次を使います。

- `traceary memory extract`

extract は candidate-only です。自動で accepted にはしません。

### 参照する経路

既存の durable memory を調べるときは次を使います。

- `traceary memory list`
- `traceary memory search`
- `traceary memory show`

### 文脈に載せる経路

Durable memory を再開向けの pack に組み込んで使いたいときは次を使います。

- `traceary handoff`
- MCP `session_handoff`
- MCP `memory_pack`

`handoff` は次の session 向けの working-memory 要約です。`memory_pack` は、durable memory を含む構造化 bundle がほしい MCP client 向けの相当物です。

## sanitization / redaction

Durable memory は長く残る文脈なので、抽出または保存する前に既存の sanitization / redaction 経路を通す前提です。

つまり:

- 長期利用の文脈としては、生の shell 出力より安全に扱いやすい
- ただし、secret を保存してよい場所ではない
- 残してはいけない情報なら durable memory に昇格させない

## 推奨ワークフロー

1. hooks や CLI で、まず audit layer に生の履歴を残す
2. `traceary tail`、`traceary list`、`traceary search`、`traceary show` で最近の流れを確認する
3. すでに信頼できる事実は `traceary memory remember` で明示的に残す
4. session summary や compact summary から review 用候補を作るときは `traceary memory extract` を使う
5. 次のエージェントや次回 session に引き継ぐときは `traceary handoff` を使う

## 関連文書

- [README](../../README.ja.md)
- [CLI リファレンス](../cli/README.ja.md)
- [MCP ガイド](../mcp/README.ja.md)
- [Hook contract](../hooks/contract.ja.md)
- [イベントライフサイクル](../lifecycle.ja.md)
