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

session metadata、recent commands、compact summary、accepted durable memories から組み立てた handoff summary を表示します。`--compact-only` を付けると、prompt injection 向けの短い summary を出力します (v0.9.0 で `traceary compact-summary` の代替として導入)。`--compact-only` 指定時は `--recent` 未指定なら 3 に自動設定されます (v0.8.x の compact-summary と同一のデフォルト)。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`
- `--preset` (任意): durable memory に built-in preset (`resume` / `review` / `incident`) を適用
- `--as-of` (任意): durable memory の validity を指定時刻 (YYYY-MM-DD または RFC3339) で評価する。既定は「現在」
- `--compact-only` (任意): prompt injection 向けの短い summary を出力 (`compact-summary` の代替)。`--recent` 未指定時は 3 に自動設定

> **v0.8 → v0.9 移行**: 旧 `traceary handoff` / `traceary compact-summary` も deprecated alias として動き続けますが、deprecation 通知が出ます。v1.0 で削除予定です。新規コードは `traceary session handoff` (必要に応じて `--compact-only`) を使ってください。

## Durable memory コマンド

### `traceary memory list`

durable memory を一覧表示します。scope flag を明示しない場合は、解決した workspace scope を既定で使います。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory search [<query>]`

全文検索と構造フィルタで durable memory を検索します。query か filter のどちらか 1 つ以上が必要です。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--limit`
- `--offset`
- `--json`

### `traceary memory show <memory-id>`

1 件の durable memory を詳細表示します。evidence ref と artifact ref も含みます。

主な flag:

- `--json`

### `traceary memory remember`

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

### `traceary memory propose`

candidate 状態の durable memory を記録します。あとで review できます。

主な flag は `memory remember` と同じですが、`--confidence` は使われません。

### `traceary memory distill`

既存の candidate ID を 1 件以上指定し、operator が指定した fact で新しい accepted durable memory を作成します。source candidate の evidence ref / artifact ref は accepted memory に union されます。Traceary が内容を書き換えたり、自動で accept したりすることはありません。

主な flag:

- `--from` — source candidate ID をカンマ区切りで指定 (複数指定可)
- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family` (いずれか必須)
- `--confidence`
- `--source`
- `--replace=keep|reject|supersede`
- `--id-only`
- `--json`

### `traceary memory extract`

対象 session の session summary、compact summary、prompt event、note / review signal から candidate durable memory を抽出します。抽出結果は candidate のみで、Traceary が自動で accept することはありません。prompt event は任意で、prompt や compact-summary event が無い場合も、利用できる signal の範囲で動作します。`--session-id` を省略した場合は、まず active session を解決し、見つからなければ workspace 内の latest session を使います。`Feedback:` / `Correction:` ラベルは、現在の最小 durable-memory taxonomy では `preference` candidate として保持されます。保存される candidate は、他の durable memory と同じ sanitization / redaction 経路を通ってから永続化されます。

主な flag:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--json`

### `traceary memory hygiene scan`

`accepted` な durable memory を走査し、store を変更せずに 3 種類の hygiene 候補を報告します。

- `redaction_hit` — 現在の redaction ルールで mask される内容が stored fact にまだ含まれているケース (例: `~/.config/traceary/config.json` に後から追加した extra pattern にヒット)。候補には `sanitized_fact` が付くため、続く `memory supersede` の置換テキストとしてそのまま使えます
- `expiry_candidate` — `--expiry-days` で指定した日数以上更新が無い memory。operator が expire を検討すべき候補
- `duplicate` — 同じ scope / fact を持つ accepted memory が 2 件以上ある場合のペア。どちらかを supersede / expire して整理する候補
- `supersede_candidate` — 同じ scope で fact の単語 Jaccard 類似度が `--similarity` (既定 0.6) 以上だが fact 自体は異なるペア。古い memory が supersede 対象、新しい memory の fact が提案される置換テキスト (`replacement_memory_id` / `replacement_fact` / `similarity`)

主な flag:

- `--workspace` — scope filter (未指定時は env/検出 workspace。空なら全 scope)
- `--expiry-days` — staleness 閾値 (既定 90 日)
- `--similarity` — supersede_candidate 検出の word-Jaccard 閾値 (0.0-1.0、0 は既定値 0.6)
- `--json` — JSON 形式で suggestion のメタデータ付きに出力

### `traceary memory hygiene apply`

`--ids` に指定した memory id について、該当する suggestion の lifecycle transition を適用します。usecase は内部で scan を再実行し、すでに解決済みの id は失敗一覧に回るので状態を黙って壊すことはありません。適用される transition:

- `redaction_hit` → `supersede`（sanitized fact に差し替え、scope / type / refs は継承）
- `expiry_candidate` → `expire`（現在時刻で失効）
- `duplicate` → `reject`（残したい方と対になる id を指定）
- `supersede_candidate` → `supersede`（新しい memory の fact に差し替え、scope / type / refs は元 memory から継承）

主な flag:

- `--ids` — 適用対象の memory id をカンマ区切りで指定 (複数指定可)
- `--expiry-days` — 内部 scan の staleness 閾値 (既定 90 日)
- `--json` — JSON 形式で id 別 transition メタデータを出力

### `traceary memory export`

accepted な durable memory をホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) に書き出します。出力は決定論的かつ冪等で、memory が変わらない限り同じバイト列を生成します。Traceary が書き出すブロックは `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` で囲まれており、`memory import instructions` で同じファイルを読み込む際に重複 candidate を作らないようになっています。

主な flag:

- `--target` — `claude` / `codex` / `gemini` のいずれか
- `--workspace` — 書き出し対象の workspace scope (未指定時は env/検出 workspace)
- `--out` — 書き出し先パス。`-` (または未指定) で stdout へ
- `--json` — 書き出しサマリを JSON で出力

### `traceary memory import instructions`

ホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) を読み、Traceary が書いた管理ブロック外の bullet を `candidate` として取り込みます。管理ブロック内はすでに store に存在するため意図的に skip します。

主な flag:

- `--source` — ファイルを書いたホスト (`claude` / `codex` / `gemini`)
- `--in` — instruction file のパス
- `--workspace` — 取り込む candidate に割り当てる workspace scope (未指定時は env/検出 workspace)
- `--json` — JSON で出力

### `traceary memory inbox`

durable memory inbox をレビューします。`list` は candidate memory を evidence / artifact ref 件数付きで一覧し、レビュアが provenance を確認してから accept できるようにします。`accept` / `reject` は `--ids` にカンマ区切りの memory id を渡し、順に処理して成功/失敗を id 単位で返すため、一部のみ失敗した batch でもどの id が transition したかが明確になります。

主な flag:

- `list` — `--workspace`, `--agent`, `--session-family`, `--type`, `--source` (manual / extracted / imported), `--limit`, `--offset`, `--json`
- `accept` — `--ids id1,id2,...` (複数指定可), `--confidence`, `--json`
- `reject` — `--ids id1,id2,...` (複数指定可), `--json`

`--source` は extraction / import 経路との相性が良い filter です。

- `--source imported` は host-native source (Codex 等、`memory import codex` 参照) から取り込まれた memory に絞ります。
- `--source extracted` は `traceary memory extract` が session signal から起こした memory に絞ります。

### `traceary memory import codex`

ローカルの Codex memory layout（既定値は `~/.codex/memories/MEMORY.md`）から durable memory の candidate を取り込みます。今リリースでは handbook 相当の `MEMORY.md` のみを読み、`raw_memories.md` や `rollout_summaries/*` は対象外です。`## User preferences` / `## Reusable knowledge` / `## Failures and how to do differently` 配下の各 bullet が `source=imported` + `status=candidate` で記録され、evidence/artifact ref として元ファイル・行範囲が付与されます。scope は Codex の `applies_to: cwd=...` から解決し、ヒントが無い場合は `--workspace` flag の値を fallback に使います。取り込み時は既存の redaction rule を必ず通し、auto-accept は行いません。再実行は冪等で、同じ scope/fact の memory が既に存在する場合（rejected/superseded/expired を含むすべての状態）は duplicate として skip するため、一度 reject した memory が自動的に resurrect することはありません。

主な flag:

- `--root` — Codex memory root (既定値は `~/.codex/memories`)
- `--workspace` — source 側に `applies_to` ヒントがない場合の fallback scope
- `--watch` — 1回で終了せず定期的に再 import を続ける
- `--interval` — `--watch` 時の polling interval（最低 1s）
- `--json`

### `traceary memory accept <memory-id>`

candidate durable memory を accept します。

主な flag:

- `--confidence`
- `--id-only`
- `--json`

### `traceary memory reject <memory-id>`

candidate durable memory を reject します。

主な flag:

- `--id-only`
- `--json`

### `traceary memory supersede <memory-id>`

accepted durable memory を新しい accepted memory で置き換えます。`--type` と scope flag を省略すると現在の memory を継承します。

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

### `traceary memory expire <memory-id>`

active な durable memory を expire します。

主な flag:

- `--at`
- `--id-only`
- `--json`

### `traceary memory graph add <from-memory-id> --to <to-memory-id> --relation <type>`

2 つの memory 間に型付き関係を記録します (v0.9.0 の graph overlay)。語彙と overlay の設計は [temporal memory architecture](../architecture/temporal-memory.ja.md) を参照してください。

主な flag:

- `--to`: 関係の対象 memory ID (必須)
- `--relation`: `supersedes` / `contradicts` / `supports` / `related-to` / `causes` (必須。未知値も forward compat のため受理)
- `--from`: validity 窓の下限 (YYYY-MM-DD または RFC3339); 既定は現在時刻
- `--to-date`: validity 窓の上限 (排他); 省略時は open-ended
- `--json`

### `traceary memory graph list`

指定 filter に一致する edge を表示します。`memory list --as-of` と同じ半開区間 `[valid_from, valid_to)` の semantics。

主な flag:

- `--memory-id`: この memory に接続する edge (source / target どちらでも) に絞る
- `--relation`: 関係種別でフィルタ
- `--as-of`: 指定時刻で validity を評価する
- `--limit`
- `--json`

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

### `traceary top`

active session を root session ごとにまとめたライブ自動更新 tree dashboard を表示します。各行には workspace（長い場合は末尾を保持して短縮）、最も具体的な agent/subagent role、記録 client、開始時刻、最新 activity 時刻、event 件数、最新 event を `<kind>: <message>` で表示します。最新 activity が `--idle` より古い session は dim 表示しますが、非表示にはしません。`q` または Ctrl-C で終了します。`traceary session tree` は静的な retrospective view のままです。

snapshot 例:

```sh
traceary top --snapshot
```

```text
4a70c526 workspace=github.com/duck8823/traceary agent=codex client=claude started=07:06:37 latest=07:06:58 events=165 last=session_ended: duration=29m21s
└── 7c91a2bf workspace=github.com/duck8823/traceary agent=worker client=claude started=07:03:12 latest=07:06:52 events=42 last=command_executed: go test ./presentation/cli
```

列:

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
- `--snapshot --json` — top 専用 snapshot contract で一回限りの JSON tree を出力。各 node には追加で `latest_event_kind` / `latest_event_message` / `latest_event_at` が含まれる。`traceary session tree --json` は独立した contract を保ち、これら field を露出しない
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

## Store 管理 (`traceary store ...`)

v0.9.0 から、store 管理コマンドは `store` namespace に集約されました。旧 top-level の `traceary init` / `traceary backup` / `traceary gc` も deprecated alias として動作し続けます (deprecation 通知あり)。v1.0 で削除予定です。

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

### `traceary integration codex install` (非推奨)

ローカル checkout した repository から、次の場所へ Codex 向け Traceary integration を入れます。

- `~/.agents/plugins`
- `~/.codex/plugins/cache/...`
- `~/.codex/config.toml`
- `~/.codex/hooks.json`

**非推奨**: Codex 公式の `/plugins` flow を優先してください（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary Plugins` → `Traceary`）。このコマンドは v0.8.0 より早くは削除しませんが、それ以降で削除予定です。詳しい移行手順は [Codex plugin ガイド](../integrations/codex-plugin.ja.md) を参照してください。

主な flag:

- `--repo-root`
- `--codex-home`
- `--marketplace-root`
- `--traceary-bin`

### `traceary integration codex uninstall`

Traceary が管理する Codex plugin cache、plugin config entry、hook entry を外します。Codex の他の設定は保持します。非推奨の `install` から移行するユーザーの cleanup 用として推奨される手順です。

主な flag:

- `--codex-home`
- `--marketplace-root`

### `traceary mcp-server`

AI クライアント連携向けに MCP サーバーを stdio で起動します。

## 関連ドキュメント

- 導入ガイド / クイックスタート: [`../../README.ja.md`](../../README.ja.md)
- 環境変数と runtime 前提: [`../environment/README.ja.md`](../environment/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップガイド: [`../backup/README.ja.md`](../backup/README.ja.md)
