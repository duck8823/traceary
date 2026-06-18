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

すべての durable memory は、lifecycle な `status` や `memory admin expire` で記録される `expires_at` とは別に、コンテンツの有効期間 `(valid_from, valid_to)` を持ちます。

- `valid_from` — fact が真として主張され始める時刻（既定値は `created_at`）
- `valid_to` — fact が真でなくなる時刻（`NULL` は open-ended）

既定の取得経路（CLI の `memory list` / `memory search`、MCP の `query_memory(action="retrieve")` / `query_memory(action="pack")` / `session_status(action="handoff")`）は `valid_to` が過去の memory を自動で隠します。つまり、いま真と主張されている memory だけが返ります。

時間移動したい場合は CLI list / search に `--as-of <timestamp>` を渡すと、その時点での `valid_from <= asOf < valid_to` で評価します。validity filter を外す `--include-expired` は、過去の決定を監査する場合などに使えます。これらは lifecycle な `status` filter と独立で、superseded / rejected の memory を見たい場合は引き続き `--status` を使ってください。

window の設定・更新は次で行います。

- CLI: `traceary memory admin set-validity <memory-id> [--from <time>] [--to <time>] [--clear-to]`
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

- `traceary memory store remember`
- `traceary memory store propose`
- `traceary memory store distill`
- `traceary memory inbox accept`
- `traceary memory inbox reject`
- `traceary memory admin supersede`
- `traceary memory admin expire`

### 抽出する経路

既存の session signal からメモリ候補を起こしたいときは次を使います。

- `traceary memory admin extract`

extract はメモリ候補だけを作ります。自動で accepted にはしません。

v0.11.0 以降、hook 経由の session 終了 (`traceary hook session <client> end`)、CLI 経由 (`traceary session --end`)、MCP `manage_session(action="end")` のいずれも、session 終了 record の commit 後に extract を best-effort で auto-fire します。Codex の `stop` turn 境界も extract を best-effort で fire します（Codex には host のセッション終了信号がなく、end のみだと Codex では extract が走らないため — #1170）。turn ごとに走りますが extractor は既存候補と重複排除するため再発火は安全です。エージェント側の明示要請なしにメモリ候補の確認キューが増え、extract のエラーは swallow されるため境界 record は決してブロックされません。

長さベースの quality filter により、短い候補 (20 rune 未満。artifact ref は除外) は `source=extracted` ではなく `source=extracted-hidden` で保存されます。hidden 行は audit 用に store に残りますが、`traceary memory inbox list` の既定 view には出ません。`--include-hidden` で surface できます。

v0.21.0 以降、曖昧さのない unified-diff metadata — hunk header と git-diff marker (`@@`、`diff --git`、`index …`、`+++ `/`--- `、`Binary files …`) — のみ auto-extraction 時に **完全に drop** します。これらの行は durable な prose には決してならないためです。明示的な `remember this:` intent は常に drop を上書きします。それ以外の noise は drop せず **hidden** (audit 用に保持され `--include-hidden` で復旧可能) のままです: 単一の `+`/`-` content 行 (CLI flag や符号で始まる durable prose、例: `-race must be enabled for Go tests` のことがある)、generated-code marker (generated file に *言及* した prose にも反応し得る loose な substring match で検出)、standalone command、review-only conclusion、work declaration、PR/round chatter。これらは下記の operator 確認付き cleanup workflow で意図的に掃除してください。この変更前に作られた候補は削除されません。

#### 候補の hygiene

`traceary sessions --snapshot --json` は `reliability.memory.candidate_hygiene` の各カウント — `stale_count`、`duplicate_count`、`fragment_like_count`、`extracted_hidden_count`、`likely_actionable_count` — を出力します。これにより operator は候補 backlog のうち実際にレビュー価値があるもの (`likely_actionable_count`) と、stale / duplicate / fragment-like / 既に hidden な noise の量を把握できます。4 つの flag カウントは重複しうるもので、snapshot の scan 上限 (`scan_limit_reached`) の影響を受けます。低価値な候補を掃除するには、まず dry-run 既定の `traceary memory inbox cleanup --quality low` でプレビューし、`--apply` を付けて match を reject します (cleanup は候補を reject するだけで、削除や auto-accept はしません)。`traceary memory admin hygiene scan` は snapshot の exact な `duplicate_count` (同一 scope・memory type・fact) を超えた similarity ベースの重複検出を追加します。

#### context boundary からの抽出

`traceary memory admin extract` は、意味のある post-compact summary と clear/reset 相当の summary event を抽出入力として扱います。compact-summary 系 event からできたメモリ候補は `source=compact-summary` になり、元 event を evidence として保持するため、accept 前に reviewer が実際の host signal を確認できます。

`manual`、`clear`、`reset` のような marker-only lifecycle signal や、context が clear された事実だけを Traceary に通知する host からはメモリ候補を作りません。`memory admin extract --debug-signals --json` では、これらは `ignored` かつ `reason=marker_only_context_boundary` として表示されます（pre-compact snapshot の場合は `pre_compact_snapshot`）。現時点では Claude の post-compact は summary text を渡せますが、clear/reset の support は host が summary body を提供できるかに依存します。host が marker だけを emit する場合、Traceary は boundary を記録・debug 表示しますが、durable memory を捏造しません。

### レビューする経路

メモリ候補が溜まってきて、accepted に昇格させる前にメモリ候補の確認キューを walk したいときは次を使います。

- `traceary memory inbox list`
- `traceary memory inbox accept <id>` (単一 id。バッチ用途は `--ids id1,id2,...`。scripted caller 向けには `--id-only` で memory id だけを stdout に出力)
- `traceary memory inbox reject <id>` (単一 id。バッチ用途は `--ids id1,id2,...`。scripted caller 向けには `--id-only` で memory id だけを stdout に出力)
- `traceary memory inbox attach <id> --evidence kind:value` (複数の `--evidence` と任意の `--artifact kind:value`) で、有用なメモリ候補に accept / distill 前の support refs を追加。artifact のみの追加は、その候補がすでに evidence を持っている場合だけ可能です。
- `traceary memory inbox review` — `inbox list` と同じフィルター (`--workspace` / `--agent` / `--session-family` / `--type` / `--source` / `--include-hidden` / `--limit`) で対話的にレビューします。accept / reject は batch コマンドと同じ application usecase を呼び出し、`r` でフォーカス中のメモリ候補にカンマ区切りの evidence ref と任意の `artifact:kind:value` ref を追加できます。`e` で開く edit プロンプトでは operator が手書きした fact のみを受け付け、`traceary memory store distill` 経由で記録します (LLM 出力を自動採用しません)。TTY が無いシェルでは exit code `2` で起動を拒否し、上記のバッチコマンドを案内するため、非対話シェルから条件分岐できます。
- MCP `memory_inbox_batch` (agent からの一括 review 用)

review 経路はメモリ候補のみを対象にしているため、extraction と import は同じメモリ候補の確認キューに合流し、1回の review pass でまとめて捌けます。

raw メモリ候補が evidence としては有用だが、そのまま accepted fact にするには粗い場合は `traceary memory store distill` を使います。distill は最終 fact・type・scope を operator が明示する必要があり、Traceary が LLM rewrite や自動 accept を行うことはありません。元のメモリ候補の evidence ref / artifact ref は union されて新しい accepted memory に引き継がれ、元のメモリ候補は `--replace=keep|reject|supersede` に従って処理されます。

例:

```sh
traceary memory store distill \
  --from memory-f332...,memory-7f83... \
  --type constraint \
  --workspace github.com/asahi-digital/delivery-platform \
  --fact 'SNS Publish error mapping must not collapse operationally important AWS SDK v2 SNS errors to unknown.' \
  --replace=supersede
```

### 取り込む経路

ローカルの別エージェントが書いた memory を、ストアを統合せずに Traceary のメモリ候補として surface したいときは次を使います。

- `traceary memory admin import codex`
- `traceary memory admin import instructions --source <claude|codex|gemini> --in <path>`

### Hygiene 経路

accepted memory layer を定期的に手入れしたいときに使います。

- `traceary memory admin hygiene scan`
- `traceary memory admin hygiene apply --ids id1,id2,...`
- MCP `query_memory(action="scan_hygiene")`

scan は accepted memory に対して 5 種類の条件をチェックします: 現在の redaction ルールで mask されるべき内容 (`redaction_hit`)、`--expiry-days` 以上更新が無い stale row (`expiry_candidate`)、同一 scope + 同一 fact の衝突 (`duplicate`)、scope を共有し単語 Jaccard 類似度が閾値を超える書き換えペア (`supersede_candidate`)、`(scope, type)` を共有し明示的な temporal validity window が重なるペア (`validity_overlap_supersede`)。validity_overlap_supersede はより具体的なシグナルで、両方の検出に該当するペアは重複表示せず `validity_overlap_supersede` 側だけ報告します。apply は `--ids` に渡した memory について該当 suggestion の lifecycle transition を commit します (`redaction_hit` は sanitized fact に supersede、`expiry_candidate` は expire、`duplicate` は reject、`supersede_candidate` / `validity_overlap_supersede` は新しい memory の fact で supersede)。MCP 側の `query_memory(action="scan_hygiene")` は read-only で、agent からも同じ hygiene 候補を確認できます。

### ブリッジ / 書き出し経路

Traceary をローカルの source of truth として保ちつつ、accepted な memory 集合をホスト側の instruction file にも反映したいときは次を使います。

- `traceary memory admin export --target <claude|codex|gemini> --out <path>`
- `traceary memory admin import instructions --source <...> --in <path>`
- MCP `query_memory(action="export")` / `manage_memory(action="import_instructions")` (agent からの呼び出し)

export 出力は常に `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` マーカーで囲まれており、続けて `memory admin import instructions` を走らせても重複したメモリ候補は作られません。operator やホストの auto-memory 機能が管理ブロック外に書き足した bullet はメモリ候補の確認キューに入り、レビュー対象になります。

workspace export は user-level の運用ルール (PR title や review policy など) も host file に載るよう、既定で `global` memory を含めます。Markdown は `Global memories` / `Workspace memories` など scope ごとに見出しを分けます。従来の workspace-only filter を維持したい場合は `--no-global` (MCP では `include_global=false`) を使い、既定挙動を明示したい場合は `--include-global` を指定します。

host-native file を変更する前段として activation planning を dry-run で使い、その後明示的に apply できます。`--target <codex|claude|gemini>` のコマンド集合は全 host で共通です。

- `traceary memory admin activate --target <host> --status` — read-only な health view
- `traceary memory admin activate --target <host> --dry-run [--diff]` — 書き込まずに content/diff を表示
- `traceary memory admin activate --target <host> --apply` — activation target に安全に書き込み

`--root` は activation root の上書き、`--path` は activation target file の上書きです。Claude / Gemini では `--path` が host context file を指し、external memory file はその directory から導出されます。apply は idempotent（accepted memory が変わらなければ再実行は no-op）で、Traceary 管理ブロック外の user-authored content を保持し、symlink / directory / 不正マーカー / 新バージョンマーカー等の安全でない target を拒否します。出力には activated memory count を含みます。status は `missing` / `stale` / `in_sync` / `invalid` を表示し、refresh が必要な場合は正確な dry-run/apply command を出力します。`traceary doctor --client <host>` は同じ activation status を `<host>-memory-activation` check として surface し、同じ remediation command を提示します。

### ホスト別 activation strategy

Traceary では 3 つの層を分けます。

1. **Accepted memory store** — local SQLite の `memories` aggregate。review 済み durable fact の source of truth です。
2. **Instruction-file export** — `traceary memory admin export --target <claude|codex|gemini>` が書く決定論的な markdown block。project/user instruction file を読むホスト向けの portable path です。
3. **Host-native activation** — accepted store をホスト固有の native memory system から見えるようにする file/write path。Traceary 管理ブロック外の user-authored content は保持します。

v0.13.0 で **Codex** / **Claude** / **Gemini** のすべてに host-native activation を提供します。CLI 表面は host を問わず同一で、解決される target path と管理 region のレイアウトだけが異なります。

| Host | 既定 activation root | 管理ファイル | 戦略 |
| --- | --- | --- | --- |
| Codex | `~/.codex/memories`（legacy native memory root） | `~/.codex/memories/traceary.md`（single-file） | Traceary 管理ファイル。apply はその中の管理ブロックだけを置換し、ブロック外の content は保持 |
| Claude | 直近の `.git` 祖先、無ければ cwd | `<root>/CLAUDE.md`（host context）+ `<root>/.traceary/memories/claude.md`（external memory） | 二ファイル pair。apply は external memory file を先に書き、`CLAUDE.md` には小さな managed import stub (`@./.traceary/memories/claude.md`) を missing / stale のときだけ書く |
| Gemini | 直近の `.git` 祖先、無ければ cwd | `<root>/GEMINI.md`（host context）+ `<root>/.traceary/memories/gemini.md`（external memory） | Claude と同形の二ファイル pair。apply は Gemini の `## Gemini Added Memories` を byte-for-byte で保持し、managed import stub は append または更新のみ |

managed marker layout、status state、root detection、tracked-file policy、却下した代替案を含む完全な契約は [host-native memory activation ADR](../architecture/host-native-memory-activation.ja.md) を参照してください。

#### host-owned auto-memory section に書かない理由

各 host にはすでに host 自身が read/write する memory 表面があります。

- Claude Code は `~/.claude/projects/<project>/memory/` を host-owned auto memory として read/write します。
- Gemini の `save_memory` tool は `~/.gemini/GEMINI.md` の `## Gemini Added Memories` 見出し配下に fact を append します。

v0.13.0 では Traceary は意図的にこれらの region に書きません。

- accepted memory store を source of truth として保つため。host 管理の事実と混ぜると、host 側がいつ rewrite してもおかしくない format に Traceary projection が密結合します。
- Traceary 管理 region 外の user-authored content は `dry-run` / `apply` / `status` / `doctor` を通っても変わらないと約束する必要があるため。Gemini smoke test は seed した `## Gemini Added Memories` section が apply 後も byte-for-byte で保持されることを明示的に検査します。
- auto-memory を書き換える代わりに、Traceary は小さな managed import stub と external memory file の pair を append します。host は native な markdown import で external memory を読むため、refresh の頻度が高い更新は基本的に external file 側に閉じ、host context file はマーカー自体が変わる時だけ touch します。

managed import stub のマーカー (`<!-- traceary-memory-import:begin:v1 -->` … `<!-- traceary-memory-import:end -->`) と external file のマーカー (`<!-- traceary-memories:begin:v1 -->` … `<!-- traceary-memories:end -->`) により、`import instructions` などのツールは bullet を重複させず、user-authored 文も壊さず round-trip できます。

#### 共通ワークフロー

1. status を確認 — `traceary memory admin activate --target <host> --status`。Claude / Gemini は project root 内で実行して、`.git` を含む最も近い ancestor へ activation root を解決させる。
2. 計画差分を確認 — `traceary memory admin activate --target <host> --dry-run --diff`。出力は component ごとにラベル（two-file target は `external memory plan` / `host context plan`）が付くので、どのファイルが変わるか確認できる。
3. 反映 — `traceary memory admin activate --target <host> --apply`。何も変わっていなければ再実行は noop に収束する。
4. 検証 — `traceary doctor --client <host>` の `<host>-memory-activation` check に同じ dry-run/apply remediation が含まれる。

status が `invalid` を返したときに apply を盲目的に再実行しないでください。よくある原因は次の "invalid からの復旧" を参照してください。

#### invalid からの復旧

`invalid` は host file または external memory file を安全に解釈できないため Traceary が書き込みを拒否した状態です。よくある原因と推奨復旧手順:

| 原因 | apply が拒否する理由 | 復旧手順 |
| --- | --- | --- |
| target が symlink または directory | safe writer が atomic rename で任意 path を辿らないよう、regular file 以外は拒否 | symlink / directory を regular file に置き換える（または削除）→ `--status` を再実行 |
| 管理ブロックが手で編集されており、マーカーが重複・孤立・不正 | どのバイトが user-authored かを判定できない | 該当ファイルを開き、元のマーカーを復旧する（または管理 region を削除する）→ `--status` と `--apply` を再実行 |
| 管理ブロックが新しい marker version で書かれている | 別マシンの新しい Traceary が知らない契約を書いている。上書きすると静かに downgrade になる | このマシンの Traceary を upgrade、または管理ブロックを削除して再 apply |
| Traceary stub の外で、expected `.traceary/memories/<host>.md` を指す unmanaged import line が既にある | apply すると import が二重になる | unmanaged line を削除（将来の明示 adopt workflow を待つ）→ apply を再実行 |
| host context file は invalid だが external file は正常（または逆） | pair の状態は集約 state で判定 | `--json` の component fields (`host_context.state` / `external_memory.state`) を見て該当ファイルを特定してから編集 |

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

- `traceary session handoff`
- MCP `session_status(action="handoff")`
- MCP `query_memory(action="pack")`

`session handoff` は次の session 向けの working-memory 要約です（v0.13.x までの top-level alias `traceary handoff` は v0.14.0 で削除されました）。`query_memory(action="pack")` は、durable memory を含む構造化 bundle がほしい MCP client 向けの相当物です。

## sanitization / redaction

Durable memory は長く残る文脈なので、抽出または保存する前に既存の sanitization / redaction 経路を通す前提です。

つまり:

- 長期利用の文脈としては、生の shell 出力より安全に扱いやすい
- ただし、secret を保存してよい場所ではない
- 残してはいけない情報なら durable memory に昇格させない

## 推奨ワークフロー

1. hooks や CLI で、まず audit layer に生の履歴を残す
2. `traceary tail`、`traceary list`、`traceary search`、`traceary show` で最近の流れを確認する
3. すでに信頼できる事実は `traceary memory store remember` で明示的に残す
4. session summary や compact summary から review 用候補を作るときは `traceary memory admin extract` を使う
5. 次のエージェントや次回 session に引き継ぐときは `traceary session handoff` を使う

## 関連文書

- [README](../../README.ja.md)
- [CLI リファレンス](../cli/README.ja.md)
- [MCP ガイド](../mcp/README.ja.md)
- [Hook contract](../hooks/contract.ja.md)
- [ライフサイクルイベント](../hooks/lifecycle-events.ja.md)
- [イベントライフサイクル](../lifecycle.ja.md)
