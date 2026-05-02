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

- `candidate`: 抽出または提案されたが、まだレビュー前の memory（lifecycle status は常に `candidate`。古いドキュメントでは「proposed」と書かれていることがあるが同義）
- `accepted`: セッションをまたいで再利用したい active memory
- `rejected`: 再利用しないと決めた candidate
- `superseded`: 新しい accepted memory に置き換えられた古い memory、または distilled accepted fact に置き換えられた reviewed candidate
- `expired`: 期限付きで有効だったが、いまは active ではない memory

既定の active-memory 系の取得では、active な accepted memory だけを対象にします。

durable memory を `type` + `scope` で分類する方針にし、別軸 `block` を入れなかった理由は [Memory blocks: 評価と決定](../architecture/memory-blocks.ja.md) を参照してください。

### コンテンツ validity window

すべての durable memory は、lifecycle な `status` や `memory expire` で記録される `expires_at` とは別に、コンテンツの有効期間 `(valid_from, valid_to)` を持ちます。

- `valid_from` — fact が真として主張され始める時刻（既定値は `created_at`）
- `valid_to` — fact が真でなくなる時刻（`NULL` は open-ended）

既定の取得経路（CLI の `memory list` / `memory search`、MCP の `query_memory(action="retrieve")` / `query_memory(action="pack")` / `session_status(action="handoff")`）は `valid_to` が過去の memory を自動で隠します。つまり、いま真と主張されている memory だけが返ります。

時間移動したい場合は CLI list / search に `--as-of <timestamp>` を渡すと、その時点での `valid_from <= asOf < valid_to` で評価します。validity filter を外す `--include-expired` は、過去の決定を監査する場合などに使えます。これらは lifecycle な `status` filter と独立で、superseded / rejected の memory を見たい場合は引き続き `--status` を使ってください。

window の設定・更新は次で行います。

- CLI: `traceary memory set-validity <memory-id> [--from <time>] [--to <time>] [--clear-to]`
- MCP: `manage_memory({"action":"set_validity","ids":"<id>","valid_from":"...","valid_to":"..."})`

`--clear-to` / `clear_valid_to` は既存の `valid_to` を外して open-ended に戻します。新しい `valid_to` との併用はできません。

## evidence ref と artifact ref の違い

Traceary は、memory に 2 種類の参照を持たせます。

- **evidence ref**: その fact を正当化する根拠
- **artifact ref**: 次に人やエージェントが開きたくなる対象

accepted memory には evidence ref が必須です。artifact ref は任意です。

### evidence ref の `kind` enum

MCP / CLI が受け入れる `evidence_refs[].kind` は次の値だけです（実装は [`domain/types/evidence_ref.go`](../../domain/types/evidence_ref.go)）。未知の値は `unknown evidence ref kind: <value>` で reject されます。

| `kind` | 意味 | 典型的な `value` |
| --- | --- | --- |
| `event` | `events.id` | `evt-abc123…` |
| `session` | `sessions.session_id` | `session-…` |
| `url` | Web URL | `https://…` |
| `file` | リポジトリ相対 / 絶対ファイルパス | `docs/memory/README.ja.md` |
| `issue` | issue 番号 | `#462` |
| `pr` | pull-request 番号 | `#468` |

### artifact ref の `kind` enum

`artifact_refs[].kind` は別 enum です（実装は [`domain/types/artifact_ref.go`](../../domain/types/artifact_ref.go)）。evidence 側と多くを共有しますが、`event` / `session` は **artifact では不可** です。

| `kind` | 典型的な `value` |
| --- | --- |
| `url` | `https://grafana.internal/...` |
| `file` | `docs/architecture/redaction.md` |
| `issue` | `#462` |
| `pr` | `#468` |

## memory コマンドの関係

### 手動で書き込む経路

人やエージェントが、残すべき事実を明示的に記録したいときは次を使います。

- `traceary memory remember`
- `traceary memory propose`
- `traceary memory distill`
- `traceary memory accept`
- `traceary memory reject`
- `traceary memory supersede`
- `traceary memory expire`

### 抽出する経路

既存の session signal から candidate memory を起こしたいときは次を使います。

- `traceary memory extract`

extract は candidate-only です。自動で accepted にはしません。

v0.11.0 以降、hook 経由の session 終了 (`traceary hook session <client> end|stop`)、CLI 経由 (`traceary session --end`)、MCP `manage_session(action="end")` のいずれも、session 終了 record の commit 後に extract を best-effort で auto-fire します。エージェント側の明示要請なしに inbox が増え、extract のエラーは swallow されるため境界 record は決してブロックされません。

長さベースの quality filter により、短い候補 (20 rune 未満。artifact ref は除外) は `source=extracted` ではなく `source=extracted-hidden` で保存されます。hidden 行は audit 用に store に残りますが、`traceary memory inbox list` の既定 view には出ません。`--include-hidden` で surface できます。

#### context boundary からの抽出

`traceary memory extract` は、意味のある post-compact summary と clear/reset 相当の summary event を抽出入力として扱います。compact-summary 系 event からできた candidate は `source=compact-summary` になり、元 event を evidence として保持するため、accept 前に reviewer が実際の host signal を確認できます。

`manual`、`clear`、`reset` のような marker-only lifecycle signal や、context が clear された事実だけを Traceary に通知する host からは candidate を作りません。`memory extract --debug-signals --json` では、これらは `ignored` かつ `reason=marker_only_context_boundary` として表示されます（pre-compact snapshot の場合は `pre_compact_snapshot`）。現時点では Claude の post-compact は summary text を渡せますが、clear/reset の support は host が summary body を提供できるかに依存します。host が marker だけを emit する場合、Traceary は boundary を記録・debug 表示しますが、durable memory を捏造しません。

### レビューする経路

candidate が溜まってきて、accepted に昇格させる前に inbox を walk したいときは次を使います。

- `traceary memory inbox list`
- `traceary memory inbox accept --ids id1,id2,...`
- `traceary memory inbox reject --ids id1,id2,...`
- MCP `memory_inbox_batch` (agent からの一括 review 用)

review 経路は `candidate` のみを対象にしているため、extraction と import は同じ inbox に合流し、1回の review pass でまとめて捌けます。

raw candidate が evidence としては有用だが、そのまま accepted fact にするには粗い場合は `traceary memory distill` を使います。distill は最終 fact・type・scope を operator が明示する必要があり、Traceary が LLM rewrite や自動 accept を行うことはありません。source candidate の evidence ref / artifact ref は union されて新しい accepted memory に引き継がれ、source candidate は `--replace=keep|reject|supersede` に従って処理されます。

例:

```sh
traceary memory distill \
  --from memory-f332...,memory-7f83... \
  --type constraint \
  --workspace github.com/asahi-digital/delivery-platform \
  --fact 'SNS Publish error mapping must not collapse operationally important AWS SDK v2 SNS errors to unknown.' \
  --replace=supersede
```

### 取り込む経路

ローカルの別エージェントが書いた memory を、ストアを統合せずに Traceary の candidate として surface したいときは次を使います。

- `traceary memory import codex`
- `traceary memory import instructions --source <claude|codex|gemini> --in <path>`

### Hygiene 経路

accepted memory layer を定期的に手入れしたいときに使います。

- `traceary memory hygiene scan`
- `traceary memory hygiene apply --ids id1,id2,...`
- MCP `query_memory(action="scan_hygiene")`

scan は accepted memory に対して 5 種類の条件をチェックします: 現在の redaction ルールで mask されるべき内容 (`redaction_hit`)、`--expiry-days` 以上更新が無い stale row (`expiry_candidate`)、同一 scope + 同一 fact の衝突 (`duplicate`)、scope を共有し単語 Jaccard 類似度が閾値を超える書き換えペア (`supersede_candidate`)、`(scope, type)` を共有し明示的な temporal validity window が重なるペア (`validity_overlap_supersede`)。validity_overlap_supersede はより具体的なシグナルで、両方の検出に該当するペアは重複表示せず `validity_overlap_supersede` 側だけ報告します。apply は `--ids` に渡した memory について該当 suggestion の lifecycle transition を commit します (`redaction_hit` は sanitized fact に supersede、`expiry_candidate` は expire、`duplicate` は reject、`supersede_candidate` / `validity_overlap_supersede` は新しい memory の fact で supersede)。MCP 側の `query_memory(action="scan_hygiene")` は read-only で、agent からも同じ hygiene 候補を確認できます。

### ブリッジ / 書き出し経路

Traceary をローカルの source of truth として保ちつつ、accepted な memory 集合をホスト側の instruction file にも反映したいときは次を使います。

- `traceary memory export --target <claude|codex|gemini> --out <path>`
- `traceary memory import instructions --source <...> --in <path>`
- MCP `query_memory(action="export")` / `manage_memory(action="import_instructions")` (agent からの呼び出し)

export 出力は常に `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` マーカーで囲まれており、続けて `memory import instructions` を走らせても重複 candidate は作られません。operator やホストの auto-memory 機能が管理ブロック外に書き足した bullet は inbox に candidate として入り、レビュー対象になります。

workspace export は user-level の運用ルール (PR title や review policy など) も host file に載るよう、既定で `global` memory を含めます。Markdown は `Global memories` / `Workspace memories` など scope ごとに見出しを分けます。従来の workspace-only filter を維持したい場合は `--no-global` (MCP では `include_global=false`) を使い、既定挙動を明示したい場合は `--include-global` を指定します。

host-native file を変更する前段として activation planning を dry-run で使い、その後明示的に apply できます。

- `traceary memory activate --target codex --dry-run`
- `traceary memory activate --target codex --apply`
- `traceary memory activate --target codex --status`

Codex の既定 target は Traceary 管理ファイル (`~/.codex/memories/traceary.md`) で、user-authored な memory shard を上書きしません。`--root` / `--path` で明示上書きでき、`--diff` で既存 target file との差分を表示できます。
apply mode は必要に応じて target directory/file を作成し、Traceary 管理ブロックだけを置換し、その外側の user-authored content は保持します。出力には activated memory count を含み、新しい marker version の管理ブロックは古い binary で上書きしません。
status mode は read-only で `missing` / `stale` / `in_sync` / `invalid` を表示し、Codex file の作成・更新が必要な場合は正確な dry-run/apply command を出力します。`traceary doctor --client codex` でも同じ activation status を確認できます。

### ホスト別 activation strategy

Traceary では 3 つの層を分けます。

1. **Accepted memory store** — local SQLite の `memories` aggregate。review 済み durable fact の source of truth です。
2. **Instruction-file export** — `traceary memory export --target <claude|codex|gemini>` が書く決定論的な markdown block。project/user instruction file を読むホスト向けの portable path です。
3. **Host-native activation** — accepted store をホスト固有の native memory system から見えるようにする file/write path。Traceary 管理ブロック外の user-authored content は保持します。

v0.12 で full host-native activation を実装しているのは **Codex** のみです。

- `traceary memory activate --target codex --dry-run`
- `traceary memory activate --target codex --status`
- `traceary memory activate --target codex --apply`

**Claude** の v0.12 推奨 workflow は、Traceary MCP tools と instruction-file export (`traceary memory export --target claude --out CLAUDE.md`) です。Anthropic SDK loop を自前で持つ場合だけ experimental な Anthropic native memory-tool backend も選べます。`memory activate --target claude` は v0.12 では未実装で、将来の safe write path は follow-up #883 で扱います。

**Gemini** の v0.12 推奨 workflow は、Traceary MCP/extension と instruction-file export (`traceary memory export --target gemini --out GEMINI.md`) です。Gemini host-native activation write は意図的に defer しており、follow-up #884 で扱います。

import は Codex の Markdown memory（既定値は `~/.codex/memories/*.md`）を読みます。legacy `MEMORY.md` は `## User preferences` / `## Reusable knowledge` / `## Failures and how to do differently` の allow-list を維持し、それ以外の Markdown shard は任意の見出し配下の bullet/list item を `source=imported` + `status=candidate` として記録します。evidence / artifact ref には元ファイルと行範囲を付与し、sanitizer は全ての imported fact に適用されます。auto-accept はされず、再実行時の dedupe は rejected / superseded / expired を含む全状態を見るので、一度 operator が reject した memory が別 run で resurrect することはありません。

### 参照する経路

既存の durable memory を調べるときは次を使います。

- `traceary memory list`
- `traceary memory search`
- `traceary memory show`

#### 取り出し preset

`memory list` / `memory search` / MCP `query_memory(action="retrieve")` は `--preset <name>` で用途別の取り出し shape をプリセットできます。`--status` / `--type` を明示した場合は preset のデフォルトを上書きします。

| Preset | 用途 | 既定フィルタ |
| --- | --- | --- |
| `resume` | 「どこまでやっていたか」を拾う。type 軸は絞らない | `status=accepted` |
| `review` | 「何を決めて、どんな制約があるか」を確認する。再読に値する long-lived 知識だけに絞る | `status=accepted`, `type=decision,constraint,artifact` |
| `incident` | 「失敗直後、何を知るべきか」。決定・制約・lesson に加えて artifact（ログ場所・ダッシュボード・runbook 等）も含める | `status=accepted`, `type=decision,constraint,lesson,artifact` |

例:

- `traceary memory list --preset review --workspace github.com/org/repo`
- `traceary memory list --preset review --type lesson` — 明示 `--type` は preset の既定を上書き
- MCP: `query_memory({"action":"retrieve","preset":"incident","workspace":"..."})`

### 文脈に載せる経路

Durable memory を再開向けの pack に組み込んで使いたいときは次を使います。

- `traceary handoff`
- MCP `session_status(action="handoff")`
- MCP `query_memory(action="pack")`

`handoff` は次の session 向けの working-memory 要約です。`query_memory(action="pack")` は、durable memory を含む構造化 bundle がほしい MCP client 向けの相当物です。

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
- [ライフサイクルイベント](../hooks/lifecycle-events.ja.md)
- [イベントライフサイクル](../lifecycle.ja.md)
