# CLI リファレンス

[English](./README.md)

このページでは、公開 CLI の挙動をコマンド別にまとめています。
導入直後は `README.ja.md` のクイックスタートと合わせて参照してください。

## 共通ルール

- DB path の解決順: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- 更新系コマンドは既定で読みやすいテキスト形式を出力します
- イベント / セッションの識別子を返すコマンドは、スクリプト向けに `--id-only` をサポートします
- 構造化出力を持つコマンドは `--json` をサポートします
- CLI 出力の JSON/NDJSON contract test は [`../operations/json-contract-tests.ja.md`](../operations/json-contract-tests.ja.md) にまとめています。

## イベント記録コマンド

### `traceary log <message>`

note event を追記します。

既定値:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / 検出した workspace
- `--session-id`: flag → `TRACEARY_SESSION_ID` → 解決した workspace の最新 non-stale active session → `default`

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`

session 解決ルール:

- 明示 `--session-id` または `TRACEARY_SESSION_ID` を最優先
- それ以外では、解決できた workspace に対応する最新の non-stale active session を再利用
- `remote.origin.url` が無い Git worktree でも、work-context key として worktree ルートパスを使います
- workspace を解決できない、または一致する active session が無い場合は、従来どおり `default` session ID を使います

> **注意:** `log` と `audit` は `--session-id` の値をそのまま受け入れ、存在確認は行いません。これは意図的な設計です。hook では高頻度にイベントを書き込むため、毎回 DB ルックアップを挟むとオーバーヘッドが大きくなります。存在しない session ID を渡した場合でもイベント自体は記録されますが、session 単位のクエリには現れません。

### `traceary audit <command> [<input>] [<output>]`

コマンド実行の監査イベントを記録します。

入力方法:

- command だけの位置引数: `traceary audit "go test ./..."`
- 位置引数: `traceary audit "go test ./..." '{}' '{}'`
- named flags: `traceary audit --command "go test ./..." --input '{}' --output '{}'`

主な flag:

- `--command`
- `--input`
- `--output`
- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

session 解決ルールは `traceary log` と同じです。

## 参照・検索コマンド

### `traceary tui`

Traceary operator cockpit TUI を開きます。

個別の subcommand を覚える代わりに、operator loop を 1 つのターミナル画面から始めたいときは、対話 terminal で bare `traceary` を使います。`traceary tui` は同じ cockpit を明示的に開く互換 entrypoint として残ります。cockpit は Tail-first で開き、active work、直近の失敗、doctor status、前回 live-tail 以降の新着 event をまとめて表示します。Sessions タブは session 中心 (session、失敗、コマンド、状態) に保ち、メモリ候補や stale memory cleanup は専用の Memory タブに置きます。cockpit から live tail、doctor details、memory inbox review へ移動できます。

`traceary tui` は対話 terminal が必要です。非 TTY で起動した場合は exit code `2` で拒否し、script 向け command (`list`、`sessions --snapshot [--json]`、`doctor --json`、`session handoff`、`memory inbox list`。恒久的な互換 path は `top --snapshot [--json]`) の利用を案内します。非 TTY の bare `traceary` は cockpit を起動せず、help と fallback guidance を表示します。

主な flag:

- `--db-path`
- `--reset-state` (起動前に cockpit の local last-seen state をリセット)

互換性:

- 対話 terminal の bare `traceary` は Tail-first cockpit をデフォルトで開きます
- 明示的な名前付き entrypoint や互換 path が必要な場合は `traceary tui` を使ってください

### `traceary list`

最近の event を一覧表示します。

`list` は直近履歴を素早く絞るためのコマンドです。kind / client / agent / session / workspace が決まっているときはこちらを使い、キーワード検索や期間条件が必要なときは `search` を使います。

デフォルトのテキスト出力は `tail` と同じコンパクトな 1 行形式 (`HH:MM:SS  kind  agent=<agent>  sess=<先頭8文字>  ws=<basename>  message`、ヘッダ無し、現地時刻) です。`--wide` で従来の 7 カラム tab 区切り表、`--utc` でテキスト出力を UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の出力を完全再現します。`--json` は従来通りです。`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > preset fields > `~/.config/traceary/config.json` の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールド: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`。`--preset <name>` で保存済みビューを適用できます。built-in は `failures` / `prompts-only` / `compact-summaries`、`read.presets` に定義したユーザー preset が同名 built-in を上書きします。明示した `--kind` / `--failures` / `--workspace` などのフラグは常に preset より優先されます。`--wide` / `--json` のときは preset の fields 指定は無視されますが、filter は有効です。`--color=auto|always|never` でコンパクト行の ANSI ハイライトを切り替えられます（既定は `auto`、`NO_COLOR` 環境変数でも無効化可、`--wide` / `--json` では適用されません）。ハイライトが有効な場合、失敗した `command_executed` は赤+太字、`prompt` と `transcript` は cyan、`compact_summary` は magenta、`session_started` / `session_ended` は dim で表示されます。

主な flag:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`

### `traceary tail`

新しい event を追跡表示します。

`tail` はイベントの流れをその場で追いかけるためのコマンドです。最初に最近の backlog を表示し、その後はローカルストアに追加される一致 event を継続して追跡します。hook が正しく動いているか、想定した session / workspace に書き込まれているか、失敗がリアルタイムで見えているかを確認したいときに向いています。`list` のように 1 回で終わらず、`search` のようなキーワード検索も行いません。`handoff` と違って working memory は組み立てず、生の event stream をそのまま表示します。

デフォルトのテキスト出力は `HH:MM:SS  kind  agent=<agent>  sess=<先頭8文字>  ws=<basename>  message` というコンパクトな 1 行形式で、約 100 カラムに収まり、タイムスタンプは現地時刻 (local time) を使用します。`--wide` で従来の 7 カラム tab 区切り形式、`--utc` でテキスト出力のタイムスタンプを UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前と完全に同一のバイト列を再現するので、既存スクリプトとの互換を保てます。`--json` を付けると改行区切り JSON（1 行 1 event）を出力し、パイプラインから逐次処理できます（JSON の時刻は UTC RFC3339Nano で `--utc` の影響を受けません）。

> コンパクト表示の session ID (`sess=<先頭8文字>`) は人間が目視する前提の短縮形です。機械処理には `--wide --utc` または `--json` を利用してください。

`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > preset fields > config.json の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールドは `traceary list` の説明を参照してください。`--preset <name>` で保存済みビューを適用できます（built-in: `failures` / `prompts-only` / `compact-summaries`、user-defined は `read.presets`）。`--follow-session <prefix>`（8 文字以上）で 1 つの session に tail を絞れます。`traceary session list` の出力から session id の先頭を貼り付ければそのまま使えます。

主な flag:

- `--kind`
- `--limit`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--follow-session`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--failures`

### `traceary search [<query>]`

全文検索と構造フィルタで event を検索します。

テキスト出力は `list` / `tail` と同じコンパクト 1 行形式 (デフォルトで現地時刻) です。`--wide` で従来の 7 カラム表、`--utc` で UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の出力を完全再現します。`--json` は従来通りです。`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > preset fields > config.json の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールドは `traceary list` の説明を参照してください。`--preset <name>` で保存済みビューを適用できます。filter を持つ preset なら free-text query なしでも検索条件が揃うので、preset-only な検索も成立します。

主な flag:

- `--kind`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--since`
- `--until`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`

### `traceary timeline`

ギャップ検出による作業タイムラインを、ワークスペース単位のアクティビティ要約付きで表示します。

`timeline` は直近のイベントをアイドルギャップ（デフォルト 15 分）で区切って連続する作業ブロックに分け、各ブロック内で workspace ごとに整列された 1 行を表示します。ワークスペース単位のアクティビティ要約は **`compact_summary` → 最初の `prompt` → kind counts** のフォールバック順で選ばれ、そのブロック内でそのワークスペースに存在するシグナルが 1 行に展開されます。デフォルトのテキスト出力は現地時刻 (local time) で、`--utc` で UTC に切り替えられます。`--json` は UTC RFC3339Nano の `start` / `end`、数値の `duration_sec`、および `workspace_breakdown` 配列 (`{workspace, event_count, kind_counts, agents, summary, summary_source}`) を出力します。

主な flag:

- `--workspace`
- `--from`
- `--to`
- `--gap` (アイドルギャップ閾値/分)
- `--limit`
- `--json`
- `--utc`

### `traceary replay`

最近のセッション・イベント・durable memory を single-file HTML で書き出します。外部スクリプト・フォント・CDN に依存しない自己完結ファイルなので、オフラインでも閲覧可能です。インシデントレビュー・週次 retrospective・CLI を持たないチームメンバーへの共有に使います。

主な flag:

- `--out` (必須) — 書き出す HTML のパス
- `--sessions` (既定 10) — 含める直近セッション数
- `--events-per-session` (既定 20) — 1 セッションに含めるイベント数
- `--memories` (既定 20) — 含める accepted memory 数
- `--timeline-blocks` (既定 20) — timeline パネルに描画するブロック数。0 以下でパネル自体を省く
- `--hotspots` (既定 10) — failure hotspot パネルに描画するクラスタ数。0 以下でパネル自体を省く

replay HTML は sessions / timeline blocks / failure hotspots / durable memories の 4 パネル + generated-at footer の構成です。timeline と hotspot パネルは `traceary timeline` / `traceary list --failures-only` と同一意味を持つので、両方の描画を相互参照できます。

例: `traceary replay --out /tmp/replay.html`

### `traceary show <event-id>`

1 件の event を詳細表示します。

主な flag:

- `--json`

### `traceary context`

別 session や別 tool に渡すために、直近の生イベント列を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--limit`
- `--json`

### `traceary session handoff`

session metadata、recent commands、compact summary、accepted durable memories から組み立てた handoff summary を表示します。`--compact-only` を付けると、prompt injection 向けの短い summary を出力します。`--compact-only` 指定時は `--recent` 未指定なら 3 に自動設定されます。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`
- `--preset` (任意): durable memory に built-in preset (`resume` / `review` / `incident`) を適用
- `--as-of` (任意): durable memory の validity を指定時刻 (YYYY-MM-DD または RFC3339) で評価する。既定は「現在」
- `--compact-only` (任意): prompt injection 向けの短い summary を出力 (`compact-summary` の代替)。`--recent` 未指定時は 3 に自動設定

> **v0.14 移行**: 旧 top-level の `traceary handoff` / `traceary compact-summary` alias は v0.14.0 で削除されました。実行すると Cobra の generic unknown-command で終了します（具体的な migration-error stub は v0.20.0 で撤去）。`traceary session handoff`（必要に応じて `--compact-only`）を使ってください。v0.14 で削除された alias 一覧は [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

## Durable memory コマンド

`traceary memory ...` は intent 別に namespace を分けています。日常 read 用途のコマンドは top-level に残し、それ以外の verb は 3 つの namespace に整理しています。flat な verb (`memory remember` / `memory accept` / `memory hygiene scan` ...) は v0.14 の 1 マイナー互換ウィンドウを経て v0.15.0 で削除されました。スクリプトやドキュメントは下記の canonical な `memory inbox` / `memory store` / `memory admin` path を使ってください。歴史的な対応表は [memory コマンド面の整理計画](../operations/memory-command-surface.ja.md) を、deprecation のルールは [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

```
memory
├── search           # 日常 read（top-level）
├── show             # 日常 read（top-level）
├── list             # 日常 read（top-level）
├── inbox            # candidate review surface
│   ├── list
│   ├── accept
│   ├── reject
│   └── review       # 対話 TUI ウォークスルー
├── store            # deliberate write/store workflows
│   ├── remember
│   ├── propose
│   └── distill
└── admin            # extraction + host 連携 + maintenance + lifecycle
    ├── extract
    ├── import { codex | instructions }
    ├── export
    ├── activate
    ├── hygiene { scan | apply }
    ├── graph { add | list }
    ├── supersede
    ├── expire
    └── set-validity
```

### 日常 read コマンド

#### `traceary memory list`

durable memory を一覧表示します。scope flag を明示しない場合は、解決した workspace scope を既定で使います。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory search [<query>]`

全文検索と構造フィルタで durable memory を検索します。query か filter のどちらか 1 つ以上が必要です。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory show <memory-id>`

1 件の durable memory を詳細表示します。evidence ref と artifact ref も含みます。

主な flag:

- `--json`

### `traceary memory inbox` — candidate review surface

メモリ候補の確認キューをレビューします。`list` はメモリ候補を confidence / review-readiness state と evidence / artifact ref 件数付きで一覧し、レビュアが provenance を確認してから accept できるようにします。`show` は単一候補の evidence-first decision card を表示します。`accept` / `reject` は positional id（対話的に打ち込む典型ケース）か、batch script / MCP caller 向けの `--ids id1,id2,...` のどちらでも受け付けます。partial batch でも id 単位の成功/失敗を返すので、どの id が transition したかが必ず分かります。`--id-only` を指定すると memory id だけが stdout に出力されます (`--json` と排他)。canonical な memory inbox surface は v0.13.x の positional-id 形式の strict superset です。

#### `traceary memory inbox list`

主な flag: `--workspace`, `--agent`, `--session-family`, `--type`, `--source` (manual / extracted / extracted-hidden / imported / remember-intent / compact-summary), `--include-hidden`, `--limit`, `--offset`, `--json`。

`--source` は extraction / import 経路との相性が良い filter です。

- `--source imported` は host-native source (Codex 等、`memory admin import codex` 参照) から取り込まれた memory に絞ります。
- `--source extracted` は `traceary memory admin extract` が session signal から起こした memory に絞ります。
- `--source extracted-hidden` は audit 用に保存された低品質自動抽出を表示します（既定では除外）。

#### `traceary memory inbox show <memory-id>`

単一のメモリ候補を evidence-first decision card として表示します。text view には candidate fact、source context、confidence / review-readiness state、evidence refs、artifact refs、利用可能な duplicate / supersede hint、accept-as-is checklist が含まれます。`memory inbox list` の `REVIEW` column が `needs-confirmation` または `blocked:no-evidence` の候補を accept する前に使ってください。

主な flag:

- 確認対象の positional `<memory-id>`
- `--json`

#### `traceary memory inbox accept <memory-id>`

メモリ候補を accept します。

主な flag:

- 単一 id 用の positional `<memory-id>`
- batch / MCP 向けの `--ids id1,id2,...`（複数指定可）
- `--confidence`
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox reject <memory-id>`

メモリ候補を reject します。

主な flag:

- 単一 id 用の positional `<memory-id>`
- batch / MCP 向けの `--ids id1,id2,...`（複数指定可）
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox attach <memory-id>`

既存のメモリ候補に evidence refs（任意で artifact refs）を追加します。review status は変更しません。accepted memory に evidence が必要なため、まだ accept / distill できない有用な候補向けの script-friendly path です。artifact のみの追加は、その候補がすでに evidence を持っている場合だけ受け付けます。

主な flag:

- 更新対象の positional `<memory-id>`
- `--evidence kind:value`（複数指定可。1つ以上必須）
- `--artifact kind:value`（複数指定可）
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox review`

共通 Bubble Tea TUI 基盤の上に乗った TTY 専用のメモリ候補確認ウォークスルーです。フィルタは `memory inbox list` と完全に同じなので、snapshot 表示と対話的レビューをフラグ調整なしで往復できます。

画面内のキー操作:

- `a` フォーカス中のメモリ候補を accept
- `x` フォーカス中のメモリ候補を reject
- `s` skip（状態は変えずカーソルだけ進める）
- `e` edit/distill — operator 自身に新しい fact を入力させ、`traceary memory store distill --replace=supersede` 経由で記録します。LLM が書いた candidate text を自動 accept することはありません
- `r` フォーカス中のメモリ候補に1件以上の evidence ref と任意の `artifact:kind:value` ref を追加。決定は保留した順に適用されるため、accept / distill の前に attach を保留してください
- `v` evidence / artifact ref を確認
- `?` ヘルプ overlay 切替
- `q` / Ctrl-C / Esc 安全に終了

非 TTY で起動した場合はエラー終了し、exit code は `2`。fallback guidance として `memory inbox list` と `memory inbox accept|reject` を案内するため、scripted shell では deterministic に分岐します。Accept / reject / evidence attach は batch 系コマンドと同じ memory usecase を呼ぶため、dedupe / status 遷移の意味づけは従来と変わりません。TUI 終了後に queued decision の一部が失敗した場合、サマリは従来通り stdout に各 `FAILED` 行を出力しつつ、コマンドは非ゼロ error を返すため、partial failure を shell が成功扱いしません。

主な flag: `--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`。

#### `traceary memory inbox cleanup`

古い / 低品質のメモリ候補を一括でプレビューまたは reject します。既定は dry-run で、`--apply` を付けると一致した候補を reject します。フィルタ: `--quality {low|normal|any}`（既定 `low`。`--quality any` はキュー全体の reject を避けるため `--older-than` が必須）、`--source`、`--type`、`--workspace`、`--agent`、`--session-family`、`--older-than` / `--newer-than`、`--include-hidden`、`--limit`。text と `--json` の出力には composition `summary`（`total` と `by_source` / `by_type` の内訳）が含まれ、`--apply` 前に batch の構成が分かります。cleanup は候補を reject するだけで、accept は evidence-first の rails を保つため上記の個別 review 系に委ねます。

### `traceary memory store` — deliberate writes

`memory store` 配下の verb はすべて durable memory row を書き込みます。row が `accepted` で着地するか `candidate` で着地するかは問いません。

#### `traceary memory store remember`

accepted な durable memory を直接記録します。

主な flag:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--id-only`
- `--json`

#### `traceary memory store propose`

candidate 状態の durable memory を記録します。あとで review できます。

主な flag は `memory store remember` と同じですが、`--confidence` は使われません。

#### `traceary memory store distill`

既存のメモリ候補 ID を 1 件以上指定し、operator が指定した fact で新しい accepted durable memory を作成します。source メモリ候補の evidence ref / artifact ref は accepted memory に union されます。Traceary が内容を書き換えたり、自動で accept したりすることはありません。

主な flag:

- `--from` — source メモリ候補 ID をカンマ区切りで指定 (複数指定可)
- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family` (いずれか必須)
- `--confidence`
- `--source`
- `--replace=keep|reject|supersede`
- `--id-only`
- `--json`

### `traceary memory admin` — extraction / host 連携 / maintenance / lifecycle

operator 向けの管理コマンドが集まる namespace です。extraction（既存 session からメモリ候補を起こす）、host 連携 I/O (`import` / `export` / `activate`)、maintenance (`hygiene` / `graph`)、accepted row を直接更新する lifecycle verb (`supersede` / `expire` / `set-validity`) をまとめてあります。

#### `traceary memory admin extract`

対象 session の session summary、compact summary、prompt event、note / review signal からメモリ候補を抽出します。抽出結果は candidate のみで、Traceary が自動で accept することはありません。prompt event は任意で、prompt や compact-summary event が無い場合も、利用できる signal の範囲で動作します。`--session-id` を省略した場合は、まず active session を解決し、見つからなければ workspace 内の latest session を使います。`Feedback:` / `Correction:` ラベルは、現在の最小 durable-memory taxonomy では `preference` candidate として保持されます。保存される candidate は、他の durable memory と同じ sanitization / redaction 経路を通ってから永続化されます。

主な flag:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--debug-signals`
- `--json`

#### `traceary memory admin import codex`

ローカルの Codex memory layout（既定値は `~/.codex/memories` 配下の `*.md`）からメモリ候補を取り込みます。legacy `MEMORY.md` は handbook allow-list (`## User preferences` / `## Reusable knowledge` / `## Failures and how to do differently`) を維持し、それ以外の Markdown shard は任意の見出し配下の bullet/list item を取り込みます。各 bullet が `source=imported` + `status=candidate` で記録され、evidence/artifact ref として元ファイル・行範囲が付与されます。scope は Codex の `applies_to: cwd=...` から解決し、ヒントが無い場合は `--workspace` flag の値を fallback に使います。取り込み時は既存の redaction rule を必ず通し、auto-accept は行いません。再実行は冪等で、同じ scope/fact の memory が既に存在する場合（rejected/superseded/expired を含むすべての状態）は duplicate として skip するため、一度 reject した memory が自動的に resurrect することはありません。

主な flag:

- `--root` — Codex memory root (既定値は `~/.codex/memories`)
- `--workspace` — source 側に `applies_to` ヒントがない場合の fallback scope
- `--watch` — 1回で終了せず定期的に再 import を続ける
- `--interval` — `--watch` 時の polling interval（最低 1s）
- `--json`

#### `traceary memory admin import instructions`

ホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) を読み、Traceary が書いた管理ブロック外の bullet を `candidate` として取り込みます。管理ブロック内はすでに store に存在するため意図的に skip します。

主な flag:

- `--source` — ファイルを書いたホスト (`claude` / `codex` / `gemini`)
- `--in` — instruction file のパス
- `--workspace` — 取り込む candidate に割り当てる workspace scope (未指定時は env/検出 workspace)
- `--json` — JSON で出力

#### `traceary memory admin export`

accepted な durable memory をホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) に書き出します。出力は決定論的かつ冪等で、memory が変わらない限り同じバイト列を生成します。Traceary が書き出すブロックは `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` で囲まれており、`memory admin import instructions` で同じファイルを読み込む際に重複メモリ候補を作らないようになっています。

主な flag:

- `--target` — `claude` / `codex` / `gemini` のいずれか
- `--workspace` — 書き出し対象の workspace scope (未指定時は env/検出 workspace)。workspace export は host-level ルールも反映されるよう、既定で `global` memory も含めます。
- `--include-global` — workspace scope と一緒に `global` memory を含める (default `true`)
- `--no-global` — opt out して明示した workspace scope のみを書き出す
- `--out` — 書き出し先パス。`-` (または未指定) で stdout へ
- `--json` — 書き出しサマリを JSON で出力

#### `traceary memory admin activate`

accepted memory を host の native context surface へ、安全な明示書き込みで activation します。`--target` の値に依らず flag セットは共通で、**Codex** / **Claude** / **Gemini** の 3 host を同じインターフェースで扱えます。host ごとに解決される target path や管理 region のレイアウトが異なります（[host-native memory activation ADR](../architecture/host-native-memory-activation.ja.md) と [durable memory ガイド](../memory/README.ja.md#ホスト別-activation-strategy)を参照）。

mode は排他で、`--status` / `--dry-run` / `--apply` のうち 1 つだけを指定します。`--diff` は `--dry-run` 時のみ有効です。

| Mode | 動作 |
| --- | --- |
| `--status` | read-only。`missing` / `stale` / `in_sync` / `invalid` を表示し、two-file target では component ごとの内訳も表示。refresh が必要なら `next_dry_run` / `next_apply` の remediation command を出力 |
| `--dry-run [--diff]` | read-only。書き込まれる予定の content を表示。`--diff` を付けると既存 target file との差分を表示。two-file target では `external memory plan` / `host context plan`（または対応する diff）でラベル付き出力 |
| `--apply` | 変更を適用。安全な writer で書き込み（lstat → symlink/directory を拒否 → 同 directory 内の temp file へ書いて atomic rename。書き込む file の parent directory のみを作成）。idempotent で再実行は noop に収束。新 marker version は拒否 |

既定 target:

- `codex` — Traceary 管理 file `~/.codex/memories/traceary.md`。single-file target で、ファイル全体が Traceary の所有
- `claude` — host context `<root>/CLAUDE.md` + external file `<root>/.traceary/memories/claude.md`。activation root は直近の `.git` 祖先、無ければ cwd
- `gemini` — host context `<root>/GEMINI.md` + external file `<root>/.traceary/memories/gemini.md`。root 解決は Claude と同じ。Gemini の `## Gemini Added Memories` セクションは byte-for-byte で保持され、managed import stub はその後ろに append される

主な flag:

- `--target` — `codex` / `claude` / `gemini`（必須）
- `--dry-run` — ファイルを作成・更新せず activation plan を表示
- `--apply` — activation target file（two-file target なら external memory file も）に書き込む
- `--status` — accepted memories と target file を書き込みなしで比較
- `--root` — activation root の上書き（Codex: memory root、Claude/Gemini: host context file を含む project root）
- `--path` — activation target file を明示上書き。Claude / Gemini では host context file を指し、external memory file は `<path の directory>/.traceary/memories/<target>.md` として導出
- `--workspace` / `--include-global` / `--no-global` — activation scope control
- `--diff` — target file が存在する場合に diff を含める (dry-run のみ)
- `--json` — activation plan / status / apply result を JSON で出力。two-file target では `host_context` / `external_memory` component の `path` / `state` / `action` / `existing`、plan 時は `markdown` / `diff` も出力

##### 例

status（read-only、Claude / Gemini は project 内で実行して、`.git` を含む最も近い ancestor へ activation root を解決させる）:

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target claude --status --json
traceary memory admin activate --target gemini --status
```

既存ファイルとの diff 込み dry-run:

```sh
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target claude --dry-run --diff
traceary memory admin activate --target gemini --dry-run --diff
```

apply（idempotent — 再実行は安全）:

```sh
traceary memory admin activate --target codex --apply
traceary memory admin activate --target claude --apply
traceary memory admin activate --target gemini --apply
```

activation root や host context file path を明示する例:

```sh
# cwd に依らず Claude activation を特定 project に固定
traceary memory admin activate --target claude --root /path/to/project --status

# Gemini の host context file を明示指定（external file は導出）
traceary memory admin activate --target gemini --path /path/to/GEMINI.md --apply
```

##### `invalid` からの復旧

`--status` が `invalid` を返した場合は、`--apply` を盲目的に再実行しないでください（apply path は同じ理由で拒否されます）。`--json` で component 単位の state を見て根本原因を直してから `--status` を再実行します。

| 原因 | 復旧手順 |
| --- | --- |
| target が symlink または directory | regular file に置き換える（または削除） |
| 管理マーカーが重複・孤立・不正 | 元のマーカーを復元、もしくは管理 region を削除 |
| 新しい marker version | ローカル Traceary を upgrade、もしくは管理ブロックを削除して再 apply |
| Traceary stub の外で expected `.traceary/memories/<host>.md` を指す unmanaged import line がある | unmanaged line を削除してから再 apply |
| host context file は `invalid` だが external file は正常（または逆） | JSON の `host_context.state` / `external_memory.state` で該当ファイルを特定してから編集 |

#### `traceary memory admin hygiene scan`

`accepted` な durable memory を走査し、store を変更せずに hygiene 候補を報告します。

- `redaction_hit` — 現在の redaction ルールで mask される内容が stored fact にまだ含まれているケース (例: `~/.config/traceary/config.json` に後から追加した extra pattern にヒット)。候補には `sanitized_fact` が付くため、続く `memory admin supersede` の置換テキストとしてそのまま使えます
- `expiry_candidate` — `--expiry-days` で指定した日数以上更新が無い memory。operator が expire を検討すべき候補
- `duplicate` — 同じ scope / fact を持つ accepted memory が 2 件以上ある場合のペア。どちらかを supersede / expire して整理する候補
- `supersede_candidate` — 同じ scope で fact の単語 Jaccard 類似度が `--similarity` (既定 0.6) 以上だが fact 自体は異なるペア。古い memory が supersede 対象、新しい memory の fact が提案される置換テキスト (`replacement_memory_id` / `replacement_fact` / `similarity`)
- `validity_overlap_supersede` — `(scope, type)` が一致し、明示 validity 窓 `[valid_from, valid_to)` がオーバーラップするペア。両方該当する場合はこちらが優先されます

主な flag:

- `--workspace` — scope filter (未指定時は env/検出 workspace。空なら全 scope)
- `--expiry-days` — staleness 閾値 (既定 90 日)
- `--similarity` — supersede_candidate 検出の word-Jaccard 閾値 (0.0-1.0、0 は既定値 0.6)
- `--json` — JSON 形式で suggestion のメタデータ付きに出力

#### `traceary memory admin hygiene apply`

`--ids` に指定した memory id について、該当する suggestion の lifecycle transition を適用します。usecase は内部で scan を再実行し、すでに解決済みの id は失敗一覧に回るので状態を黙って壊すことはありません。適用される transition:

- `redaction_hit` → `supersede`（sanitized fact に差し替え、scope / type / refs は継承）
- `expiry_candidate` → `expire`（現在時刻で失効）
- `duplicate` → `reject`（残したい方と対になる id を指定）
- `supersede_candidate` / `validity_overlap_supersede` → `supersede`（新しい memory の fact に差し替え、scope / type / refs は元 memory から継承）

主な flag:

- `--ids` — 適用対象の memory id をカンマ区切りで指定 (複数指定可)
- `--expiry-days` — 内部 scan の staleness 閾値 (既定 90 日)
- `--json` — JSON 形式で id 別 transition メタデータを出力

#### `traceary memory admin graph add <from-memory-id> --to <to-memory-id> --relation <type>`

2 つの memory 間に型付き関係を記録します（v0.9.0 で導入された graph overlay）。語彙と overlay の設計は [temporal memory architecture](../architecture/temporal-memory.ja.md) を参照してください。

主な flag:

- `--to`: 関係の対象 memory ID (必須)
- `--relation`: `supersedes` / `contradicts` / `supports` / `related-to` / `causes` (必須。未知値も forward compat のため受理)
- `--from`: validity 窓の下限 (YYYY-MM-DD または RFC3339); 既定は現在時刻
- `--to-date`: validity 窓の上限 (排他); 省略時は open-ended
- `--json`

#### `traceary memory admin graph list`

指定 filter に一致する edge を表示します。`memory list --as-of` と同じ半開区間 `[valid_from, valid_to)` の semantics。

主な flag:

- `--memory-id`: この memory に接続する edge (source / target どちらでも) に絞る
- `--relation`: 関係種別でフィルタ
- `--as-of`: 指定時刻で validity を評価する
- `--limit`
- `--json`

#### `traceary memory admin supersede <memory-id>`

accepted durable memory を新しい accepted memory で置き換えます。`--type` と scope flag を省略すると現在の memory を継承します。

主な flag:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--from` / `--to` — 置き換え後の content validity 窓
- `--id-only`
- `--json`

#### `traceary memory admin expire <memory-id>`

active な durable memory を expire します。

主な flag:

- `--at`
- `--id-only`
- `--json`

#### `traceary memory admin set-validity <memory-id>`

durable memory の content validity 窓 (`valid_from` / `valid_to`) を設定または更新します。validity 窓は fact が真として主張される期間で、`memory admin expire` が記録する lifecycle `expires_at` とは別の軸です。

主な flag:

- `--from` — 開始 (`YYYY-MM-DD` または RFC3339)
- `--to` — 終了
- `--clear-to` — 既存の `valid_to` を外して open-ended に戻す（`--to` と排他）
- `--id-only`
- `--json`

### 削除済み flat alias (v0.15)

旧 release の flat memory verb は v0.14.x の間 hidden deprecated alias として残していましたが、v0.15.0 で削除されました。以下は歴史的な移行メモです。新しいスクリプトやドキュメントでは使用しないでください。

| 削除済み alias (v0.15) | Canonical 置き換え |
| --- | --- |
| `memory accept <id>` | `memory inbox accept <id>` |
| `memory reject <id>` | `memory inbox reject <id>` |
| `memory remember` | `memory store remember` |
| `memory propose` | `memory store propose` |
| `memory distill` | `memory store distill` |
| `memory extract` | `memory admin extract` |
| `memory import codex` | `memory admin import codex` |
| `memory import instructions` | `memory admin import instructions` |
| `memory export` | `memory admin export` |
| `memory activate` | `memory admin activate` |
| `memory hygiene scan` | `memory admin hygiene scan` |
| `memory hygiene apply` | `memory admin hygiene apply` |
| `memory graph add` | `memory admin graph add` |
| `memory graph list` | `memory admin graph list` |
| `memory supersede` | `memory admin supersede` |
| `memory expire` | `memory admin expire` |
| `memory set-validity` | `memory admin set-validity` |

## Session コマンド

### `traceary session start`

session start 境界を記録し、session ID を出力します。

既定値:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / 検出した workspace
- `--session-id`: 省略時は新しい ID を採番

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--parent-session-id`
- `--id-only`
- `--json`

### `traceary session end`

session end 境界を記録し、生成された event ID を出力します。

既定値:

- `--session-id`: flag → `TRACEARY_SESSION_ID`
- `--client` / `--agent` / `--workspace` の不足分は、対応する `session start` から補完できる場合は補完

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--summary`
- `--id-only`
- `--json`

<a id="traceary-top"></a>

### `traceary sessions`

ワークスペースの状況をライブ multi-pane dashboard で表示します。画面は active sessions (root → child) / 直近の失敗 / 直近の `command_executed` / メモリ候補の確認キュー内のメモリ候補 / stale durable memory の 5 ペイン構成で、それぞれ独立にスクロールできます。`tab` / `shift+tab` でフォーカスペイン切替、`↑/↓` (`k/j`) で 1 行スクロール、`pgup/pgdn` でページング、`g/G` で先頭 / 末尾へ、`r` で snapshot 再取得、`?` でヘルプ overlay 切替、`q` / Ctrl-C は終了します。`/` はフォーカス中ペインの incremental search prompt を開き、Enter で現在の filter を保持、Esc で active filter をクリアします。highlight 中の行で Enter を押すと、session / event / memory の detail modal を開きます。modal は Esc / `q` で閉じ、Ctrl-C は dashboard 全体を終了します。最新 activity が `--idle` より古い session は sessions ペイン内で dim 表示しますが、非表示にはしません。非 TTY 呼び出し (パイプ / CI ログ) では snapshot text 出力に自動でフォールバックします。`traceary top` は恒久的な互換 alias として引き続き使え、`traceary session tree` は静的な retrospective view のままです。

snapshot 出力は dashboard の各ペインに合わせて拡張されており、パイプ / CI / スクリプト経由の利用者も live view と同じデータを取得できます。テキスト snapshot は先頭の `RELIABILITY` セクションに続いて `ACTIVE SESSIONS` / `RECENT FAILURES` / `RECENT COMMANDS` / `CANDIDATE MEMORIES (count=N remember_intent=M)` / `STALE MEMORIES (count=N)` の各セクションに分かれ、空のペインも安定した empty-state 行を 1 行出すためヘッダーは常に出力されます。JSON snapshot は `sessions` / `failures` / `recent_commands` / `candidates` (`{ count, remember_intent_count, items }`) / `stale_memories` (`{ count, items }`) / `reliability` を持つ envelope オブジェクトでラップされています。各ペインの行上限は dashboard と揃えており (failures 50 / recent commands 50 / candidates 25 / stale memories 25)、session ペインは引き続き `--limit` を使用します。

snapshot 例:

```sh
traceary sessions --snapshot
```

```text
RELIABILITY:
- stale_active_sessions=0 hint="ok"
- memory_counts accepted=3 candidate=1 accepted_ratio=75% hint="review memory candidates with `traceary memory inbox review` and cleanup old candidates with `traceary memory inbox cleanup --dry-run`"
- candidate_age count=1 oldest=2026-04-10T12:00:00Z newest=2026-04-10T12:00:00Z avg_age=6h0m hint="prioritize older memory candidates first"
- large_payloads count=0 recent_commands=0 recent_failures=0 sampled=2 body_limit=500 hint="inspect full payloads with `traceary show <event_id>`; keep command output concise for handoff/top surfaces"

ACTIVE SESSIONS:
4a70c526 name="github.com/duck8823/traceary · codex" workspace=github.com/duck8823/traceary agent=codex client=claude started=07:06:37 latest=07:06:58 events=165 last=transcript: investigating failing tests
└── 7c91a2bf name="github.com/duck8823/traceary · worker" workspace=github.com/duck8823/traceary agent=worker client=claude started=07:03:12 latest=07:06:52 events=42 last=command_executed: go test ./presentation/cli

RECENT FAILURES:
07:06:58 command_executed go test ./presentation/cli [exit=1]

RECENT COMMANDS:
07:06:52 command_executed go build ./...

CANDIDATE MEMORIES (count=1 remember_intent=1):
mem-1 preference prefer table-driven subtests

STALE MEMORIES (count=1):
mem-stale-1 decision workspace:duck8823/traceary superseded superseded rollout note
```

active session の列:

- `name` — raw metadata の前に挿入される operator 向け表示名。`label`、`summary`、`workspace · agent`、workspace、agent、短い session id の順で fallback する。Go の `%q` と同じ quote / escape を使い、message と同じ truncate rule で短縮する
- `workspace` — 短縮した workspace path。truncate 時は末尾を保持して repo 識別子が読めるようにする
- `agent` — 最も具体的な agent / subagent role
- `client` — 記録 client
- `started` — session 開始時刻
- `latest` — latest event 時刻
- `events` — event 件数
- `last` — latest event を `<kind>: <message>` で表示。event が無い場合は `-`（message は改行 / 制御文字を scrub し、80 runes で truncate）
- `idle` — latest activity が `--idle` より古い場合に付与

主な flag:

- `--workspace`
- `--client`
- `--agent`
- `--idle <duration>` — threshold より古い行を非表示にせず dim 表示
- `--snapshot --json` — sessions 専用 snapshot contract で `sessions` / `failures` / `recent_commands` / `candidates` (`{ count, remember_intent_count, items }`) / `stale_memories` (`{ count, items }`) / `reliability` の envelope を一回限り出力します。各 session node には標準の session フィールドに加えて `latest_event_kind` / `latest_event_message` / `latest_event_at` が含まれます。failures と recent_commands は標準 event JSON、memory candidates は durable memory summary JSON と同じ shape を再利用します。stale memories は durable memory summary に `reason` を加えた shape です。`traceary session tree --json` は独立した contract を保ち、これらいずれも露出しません
- `--limit`

### `traceary session list`

session の一覧サマリーを表示します。

`session list` では、status / duration / 集計件数に加えて、`label`、`summary`、`parent_session_id` も確認できます。

主な flag:

- `--workspace`
- `--agent`
- `--label`
- `--from`
- `--to`
- `--limit`
- `--offset`
- `--json`

### `traceary session tree`

読み込んだ session の parent → child → grandchild 階層を描画します。各行は session id / status / 最も具体的な subagent role (例: Claude Code の subagent なら `claude/Explore`) / workspace / duration / `N cmds/M events` breakdown を表示します。同じ親を持つ child は `spawn_order` 昇順で並び、`spawn_order` を持たない top-level session は `started_at` 順で並びます。JSON 出力では各ノードに `parent_session_id` / `spawn_event_id` / `subagent_kind` / `spawn_order` / `depth` / 数値の `duration_sec` / `subagent_type` を追加し、外部ツールが lineage を扱えるようにしています。

主な flag:

- `--workspace`
- `--limit`
- `--root <session-id>` — 指定 session を root とするサブツリーのみ表示
- `--ongoing-only` — active session を含む lineage だけを残す
- `--json`

### `traceary session lineage <session-id>`

指定 session を含む lineage 全体を表示します。Traceary は `<session-id>` から最上位の ancestor まで上にたどり、その root と全 descendant を `session tree` と同じ text / JSON node 形式で返します。

主な flag:

- `--json`

### `traceary session label <label-text>`

session の label を設定または更新します。

既定値:

- `--session-id`: flag → `TRACEARY_SESSION_ID`

主な flag:

- `--session-id`
- `--db-path`

### `traceary session latest`

条件に一致する最新 session ID を表示します。

ここでの「最新」は、一致した session のうち最新の lifecycle boundary (`session start` または `session end`) が最も新しいものです。

主な flag:

- `--client`
- `--agent`
- `--workspace`
- `--json`

### `traceary session active`

条件に一致する active session ID を表示します。

主な flag:

- `--client`
- `--agent`
- `--workspace`
- `--stale-after`
- `--allow-stale`
- `--json`

### Session status の値

`session list` / `session tree` と `sessions --snapshot` / `top --snapshot` の JSON `status` フィールドは以下のいずれかを表示します。

| Status | 意味 |
|--------|------|
| `active` | end marker がなく stale window 内。 |
| `stale` | end marker がないが stale window（default 24h）より前に開始。 |
| `ended` | end marker があり、その後にイベントがない。 |
| `ended_with_late_events` | end marker があるが、同じ session で後続イベントが到着した。end marker は `session_ended` イベント由来、または `session gc` が `ended_at` を直接書き込んだものの場合がある。 |

active-only snapshot は `active` / `ended_with_late_events` と（`--allow-stale` 指定時の）`stale` を残します。`ended_with_late_events` は、session が既に close されているのに workspace の直近イベントが存在するとき `sessions --snapshot` が 0 件を返さないようにするための値です（例: Codex のような host が session を早期に close したが会話は継続していた場合）。CLI snapshot と MCP `session_status(action="active")` は同じルールを適用するため、end marker 後にイベントがある session は両方で surface されます。

## Hooks と診断

### `traceary completion <bash|zsh|fish|powershell>`

interactive 利用向けの shell completion script を生成します。

### `traceary hooks print`

対応クライアント向けの生成済み hook 設定を出力します。

対応 client: `claude`, `codex`, `gemini`
alias: `claude-code`, `codex-cli`, `gemini-cli`

主な flag:

- `--client`
- `--traceary-bin`

### `traceary hooks install`

対応クライアントの標準設定パスに生成済み hook 設定を書き出します。

主な flag:

- `--client`
- `--project-dir`
- `--traceary-bin`
- `--output`
- `--global` (user-level 設定へ書き込む。`--output` とは排他)
- `--force`

### `traceary hooks guide`

対応クライアントごとの install / check / verify 手順を出力します。

主な flag:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

DB アクセス、生成済み hook 設定の有無、MCP 登録、plugin version の整合性、クライアント設定のつながりを診断します。

text 出力は `Environment`、`Database`、`Plugins`、`MCP`、`Hooks` の安定した section に分かれます。
各 check は `PASS` / `WARN` / `FAIL` の severity を持ちます。`WARN` は hooks 未導入などの初回状態や未設定状態、`PATH` 上に複数の `traceary` がある状態、MCP 登録が古い binary を指す状態、plugin version が実行中の `traceary` と一致しない状態を表します。`FAIL` は DB アクセス不良、unreadable / invalid config、`PATH` 上に `traceary` がない状態のような壊れた状態を表します。

追加の doctor check:

- `path`: `PATH` 上の `traceary` 解決先と directory を確認します。見つからない場合は `FAIL`、複数見つかる場合は `WARN` です。
- `<client>-mcp`: Claude Code / Codex / Gemini の config または plugin が `traceary mcp-server` MCP server を登録しているか確認します。
- `<client>-plugin-version`: 検出した plugin manifest / cache の version と実行中 binary version を比較し、不一致なら plugin の reinstall / update を促します。
- `codex-memory-activation` / `claude-memory-activation` / `gemini-memory-activation`: accepted durable memory が host の native activation target で `missing` / `stale` / `in_sync` / `invalid` のどれかを確認します。`missing` / `stale` は `WARN` で、正確な `memory admin activate --dry-run --diff`（preview）と `memory admin activate --apply`（refresh）の remediation command を表示します。`invalid` は `FAIL` で、host file を確認してから apply するよう hint を出します。`--client <claude|codex|gemini>` で対象を絞り、`--project-dir <dir>` で Claude/Gemini の activation root を doctor process の cwd ではなく特定 repository に固定できます。

終了コード:

- `0`: すべての check が `PASS`
- `1`: 1 件以上の check が `FAIL`
- `2`: `FAIL` はないが、1 件以上の check が `WARN`

`--json` は legacy top-level `checks` を維持しつつ、sectioned structure を追加します。

```json
{
  "sections": [
    {
      "name": "Environment",
      "checks": [
        {"name": "config", "severity": "PASS", "section": "Environment", "message": "...", "hint": "", "fix_command": ""}
      ]
    }
  ],
  "summary": {"pass": 3, "warn": 1, "fail": 0},
  "exit_code": 2
}
```

alias:

- `traceary status`

主な flag:

- `--client`
- `--project-dir`
- `--json`
- `--strict` — audit-reliability: 時間に関係なく完全一致する duplicate group をすべて報告する（near-simultaneous な書き込みだけに限定しない）

## Store 管理 (`traceary store ...`)

store 管理コマンドは `store` namespace に集約されています。旧 top-level の `traceary init` / `traceary backup` / `traceary gc` alias は v0.14.0 で削除されました。実行すると Cobra の unknown-command エラーになります（`traceary store init` / `traceary store backup ...` / `traceary store gc` を使ってください）。これらの alias は v0.9.0 から v0.13.x まで deprecation 通知付きで動作していました。詳細は [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

### `traceary store init`

DB 作成と migration 適用を明示的に先行実行します。通常コマンドでも必要に応じて初期化されるため、必須ではありません。

### `traceary store backup create`

コンパクトな SQLite バックアップファイルを作成します。

主な flag:

- `--output`
- `--db-path`
- `--force`

### `traceary store backup restore`

バックアップファイルから DB を復元します。

主な flag:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary store gc`

保持期間を過ぎたストアレコードを削除し、SQLite ストアを圧縮します。既定では `--target all` により、events、空になった終了済み sessions、expired/superseded memories、終了済み memory_edges に retention を適用します。従来の event のみの動作にしたい場合は `--target events` を指定します。

主な flag:

- `--keep-days`
- `--target events|sessions|memories|memory_edges|all`
- `--dry-run`

## Integration コマンド

> `integration` コマンド subtree 全体（`integration` 親と `codex` group）は v0.20.0 時点で `traceary --help` から非表示になり、v0.21.0 で完全削除予定です。以下は移行メモとしてのみ掲載しています。非表示の stub は引き続き非ゼロで終了し、Codex 公式の `/plugins` flow を案内します。

### `traceary integration codex install` (廃止・非表示)

v0.14.0 で廃止されており、**サポート対象の install 面ではありません**。コマンドは非表示扱いとなり、実行しても install は行われず、Codex 公式の `/plugins` flow を案内するヒントのみを返します。新規 install は必ず Codex 公式の `/plugins` flow（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary Plugins` → `Traceary`）を経由してください。詳細は [Codex plugin ガイド](../integrations/codex-plugin.ja.md) を参照してください。

### `traceary integration codex uninstall` (v0.15 で削除)

v0.15.0 で削除されており、**サポート対象の uninstall 面ではありません**。この名前は歴史的な移行メモとしてのみ掲載しています。今後の uninstall は Codex 公式の `/plugins` flow を使い、v0.14 以前の旧 install 経路が残した state だけ [Codex plugin ガイドの手動 cleanup 手順](../integrations/codex-plugin.ja.md) で片付けてください。

### `traceary mcp-server`

AI クライアント連携向けに MCP サーバーを stdio で起動します。

## 関連ドキュメント

- 導入ガイド / クイックスタート: [`../../README.ja.md`](../../README.ja.md)
- 環境変数と runtime 前提: [`../environment/README.ja.md`](../environment/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップガイド: [`../backup/README.ja.md`](../backup/README.ja.md)
