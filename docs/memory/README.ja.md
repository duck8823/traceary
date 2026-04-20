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

durable memory を `type` + `scope` で分類する方針にし、別軸 `block` を入れなかった理由は [Memory blocks: 評価と決定](../architecture/memory-blocks.ja.md) を参照してください。

### コンテンツ validity window

すべての durable memory は、lifecycle な `status` や `memory expire` で記録される `expires_at` とは別に、コンテンツの有効期間 `(valid_from, valid_to)` を持ちます。

- `valid_from` — fact が真として主張され始める時刻（既定値は `created_at`）
- `valid_to` — fact が真でなくなる時刻（`NULL` は open-ended）

既定の取得経路（CLI の `memory list` / `memory search`、MCP の `retrieve_memories` / `memory_pack` / `session_handoff`）は `valid_to` が過去の memory を自動で隠します。つまり、いま真と主張されている memory だけが返ります。

時間移動したい場合は CLI list / search に `--as-of <timestamp>` を渡すと、その時点での `valid_from <= asOf < valid_to` で評価します。validity filter を外す `--include-expired` は、過去の決定を監査する場合などに使えます。これらは lifecycle な `status` filter と独立で、superseded / rejected の memory を見たい場合は引き続き `--status` を使ってください。

window の設定・更新は次で行います。

- CLI: `traceary memory set-validity <memory-id> [--from <time>] [--to <time>] [--clear-to]`
- MCP: `set_memory_validity(memory_id, valid_from?, valid_to?, clear_valid_to?)`

`--clear-to` / `clear_valid_to` は既存の `valid_to` を外して open-ended に戻します。新しい `valid_to` との併用はできません。

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

### レビューする経路

candidate が溜まってきて、accepted に昇格させる前に inbox を walk したいときは次を使います。

- `traceary memory inbox list`
- `traceary memory inbox accept --ids id1,id2,...`
- `traceary memory inbox reject --ids id1,id2,...`
- MCP `memory_inbox_batch` (agent からの一括 review 用)

review 経路は `candidate` のみを対象にしているため、extraction と import は同じ inbox に合流し、1回の review pass でまとめて捌けます。

### 取り込む経路

ローカルの別エージェントが書いた memory を、ストアを統合せずに Traceary の candidate として surface したいときは次を使います。

- `traceary memory import codex`
- `traceary memory import instructions --source <claude|codex|gemini> --in <path>`

### Hygiene 経路

accepted memory layer を定期的に手入れしたいときに使います。

- `traceary memory hygiene scan`
- `traceary memory hygiene apply --ids id1,id2,...`
- MCP `scan_memory_hygiene`

scan は accepted memory に対して 3 種類の条件をチェックします: 現在の redaction ルールで mask されるべき内容 (`redaction_hit`)、`--expiry-days` 以上更新が無い stale row (`expiry_candidate`)、同一 scope + 同一 fact の衝突 (`duplicate`)。apply は `--ids` に渡した memory について該当 suggestion の lifecycle transition を commit します (`redaction_hit` は sanitized fact に supersede、`expiry_candidate` は expire、`duplicate` は reject)。MCP 側の `scan_memory_hygiene` は read-only で、agent からも同じ hygiene 候補を確認できます。

### ブリッジ / 書き出し経路

Traceary をローカルの source of truth として保ちつつ、accepted な memory 集合をホスト側の instruction file にも反映したいときは次を使います。

- `traceary memory export --target <claude|codex|gemini> --out <path>`
- `traceary memory import instructions --source <...> --in <path>`
- MCP `export_memories` / `import_memory_instructions` (agent からの呼び出し)

export 出力は常に `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` マーカーで囲まれており、続けて `memory import instructions` を走らせても重複 candidate は作られません。operator やホストの auto-memory 機能が管理ブロック外に書き足した bullet は inbox に candidate として入り、レビュー対象になります。

import は Codex の handbook（既定値は `~/.codex/memories/MEMORY.md`）を読み、`## User preferences` / `## Reusable knowledge` / `## Failures and how to do differently` 配下の各 bullet を `source=imported` + `status=candidate` として記録します。evidence / artifact ref には元ファイルと行範囲を付与し、sanitizer は全ての imported fact に適用されます。auto-accept はされず、再実行時の dedupe は rejected / superseded / expired を含む全状態を見るので、一度 operator が reject した memory が別 run で resurrect することはありません。

### 参照する経路

既存の durable memory を調べるときは次を使います。

- `traceary memory list`
- `traceary memory search`
- `traceary memory show`

#### 取り出し preset

`memory list` / `memory search` / MCP `retrieve_memories` は `--preset <name>` で用途別の取り出し shape をプリセットできます。`--status` / `--type` を明示した場合は preset のデフォルトを上書きします。

| Preset | 用途 | 既定フィルタ |
| --- | --- | --- |
| `resume` | 「どこまでやっていたか」を拾う。type 軸は絞らない | `status=accepted` |
| `review` | 「何を決めて、どんな制約があるか」を確認する。再読に値する long-lived 知識だけに絞る | `status=accepted`, `type=decision,constraint,artifact` |
| `incident` | 「失敗直後、何を知るべきか」。決定・制約・lesson に加えて artifact（ログ場所・ダッシュボード・runbook 等）も含める | `status=accepted`, `type=decision,constraint,lesson,artifact` |

例:

- `traceary memory list --preset review --workspace github.com/org/repo`
- `traceary memory list --preset review --type lesson` — 明示 `--type` は preset の既定を上書き
- MCP: `retrieve_memories({"preset":"incident","workspace":"..."})`

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
