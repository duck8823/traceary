# ストレージモデル

[English](./README.md)

Traceary は、ローカル状態を 1 つの SQLite DB ファイルに保存します。
このガイドでは、何がどこに保存されるのか、現在の schema がどう構成されているのか、`gc` / backup の既定動作が実際に何を意味するのかを整理します。

## ローカルファーストの配置

- 既定の DB path: `~/.config/traceary/traceary.db`
- 上書き方法: `--db-path` または `TRACEARY_DB_PATH`
- file permission: parent directory は `0700`、DB file は `0600` で作成
- 外部のホスト型サービスは使わない: CLI / hooks / MCP server は同じローカル SQLite ファイルを読み書きする

`traceary store init` は任意です。ストアが必要なコマンドは、必要に応じて DB を作成し、migration を自動適用します。

## 現在の schema

現在の Traceary は次の table を作成します。

### `events`

追記専用の event stream です。note、session 境界、review、prompt、compact summary、command audit の元イベントはすべてここに入ります。

主な column:

- `id`: event identifier
- `kind`: `note`、`command_executed`、`session_started`、`session_ended`、`prompt`、`compact_summary` などの event kind
- `agent`: `codex`、`claude`、`gemini`、`manual` などの論理的な actor
- `session_id`: session grouping identifier
- `body`: 人が読むための event メッセージ
- `created_at`: RFC3339 timestamp
- `client`: `cli`、`claude`、`codex`、`gemini`、`mcp` などの ingestion path
- `workspace`: 利用可能な場合の補助的な work-context identifier

主な index:

- `idx_events_session_created_at` on `(session_id, created_at)`
- `idx_events_session_created_at_id_desc` on `(session_id, created_at DESC, id DESC)`
- `idx_events_created_at` on `(created_at DESC, id DESC)`
- `idx_events_workspace_created_at` on `(workspace, created_at)`

### `command_audits`

`command_executed` event に紐づく構造化 audit detail です。

主な column:

- `event_id`: primary key かつ `events.id` への foreign key
- `command_text`: 記録した command line
- `input_text`: 保存した command input payload
- `output_text`: 保存した command output payload
- `input_truncated`: input を切り詰めたかどうか
- `output_truncated`: output を切り詰めたかどうか
- `input_original_bytes`: `input_truncated` が true で既知の場合の元 input byte 数
- `output_original_bytes`: `output_truncated` が true で既知の場合の元 output byte 数
- `exit_code`: 取得できた場合の終了コード
- `failed`: 構造的な失敗フラグ。host が hook payload に数値 exit code を出さずに tool/command の失敗を伝える場合（例: Claude の `PostToolUseFailure`）に立つ。`list --failures` は非ゼロ `exit_code` に加えて `failed = 1` も対象にする

`input_truncated` または `output_truncated` が true の場合、保存済み payload はすでに上限内の head/tail projection であり、新規 row では対応する `*_original_bytes` column に元の byte 数を記録します。body にも人が読める文脈として `original_bytes` marker を含めます。省略された byte は過去 row から復元できません。

`command_audits.event_id` は `ON DELETE CASCADE` なので、`gc` で event を削除すると対応する audit payload も同時に消えます。

### `sessions`

start/end event から導かれ、session 系コマンドでも更新される session 集約です。

主な column:

- `session_id`: session identifier
- `started_at`: session 開始時刻
- `ended_at`: session が終了済みならその時刻
- `client`: session の client attribution
- `agent`: session の agent attribution
- `workspace`: 補助的な work-context identifier
- `label`: 任意の運用ラベル
- `summary`: 任意の session summary
- `parent_session_id`: 任意の親 session link

主な index:

- `idx_sessions_started_at`
- `idx_sessions_repo_started_at`
- `idx_sessions_parent`

### `memories`

`v0.5.0` で導入した durable memory の集約です。

主な column:

- `id`: durable memory identifier
- `type`: `decision`、`constraint`、`preference`、`lesson`、`artifact` などの taxonomy
- `scope_kind` / `scope_value`: persistence 用に平坦化した typed scope（`workspace`、`agent`、`session_family`）
- `fact`: 抽出・保持する durable-memory の本文
- `status`: `candidate`、`accepted`、`rejected`、`superseded`、`expired` などの lifecycle status
- `confidence`: `low`、`medium`、`high`、`verified` などの confidence
- `source`: `manual` や `extracted` などの source attribution
- `supersedes_memory_id`: 置き換え元 memory がある場合の参照
- `expires_at`: expiry timestamp
- `created_at` / `updated_at`: lifecycle timestamp

主な index:

- `idx_memories_scope_status_updated`
- `idx_memories_type_status_updated`
- `idx_memories_supersedes_memory_id`

### `memory_evidence_refs`

Durable memory に紐づく evidence ref です。

主な column:

- `memory_id`: `memories.id` への foreign key
- `ordinal`: memory 内での安定した順序
- `ref_kind`: `event`、`session`、`url`、`file`、`issue`、`pr` などの参照種別
- `ref_value`: 参照先の値

### `memory_artifact_refs`

Durable memory に紐づく artifact ref です。

主な column:

- `memory_id`: `memories.id` への foreign key
- `ordinal`: memory 内での安定した順序
- `ref_kind`: `file`、`url`、`command` などの artifact 種別
- `ref_value`: artifact の値

## Traceary が保存しないもの

現在の non-goals:

- SQLite file 以外に置く background daemon metadata
- hidden な cloud sync や hosted history service
- line-oriented export format を主 persistence layer にすること
- `schema/sqlite/migrations` に埋め込まれた SQL 以外の migration registry

## migration と互換性

- migration は `schema/sqlite/migrations` からバイナリに埋め込みます
- 通常コマンドの実行前に store initialization が走るため、upgrade 時も migration は自動適用されます
- backup restore では、まず SQLite file をコピーし、その後に store initialization を再実行して newer migration を適用します

任意の手動 schema edit との後方互換は保証しません。持ち運べるコピーが必要な場合は、DB を直接編集する代わりに `traceary store backup create` を使ってください。

## `gc` の既定動作

`traceary store gc` は、local store data を retention ベースで掃除するコマンドです。

- 既定 retention: `90` 日 (`--keep-days 90`)
- 既定 target: `all` (`--target all`)
- 利用可能な target: `events`, `sessions`, `memories`, `memory_edges`, `all`
- `--dry-run`: 同じ削除計画を rollback される transaction 内で実行し、候補件数だけ表示
- 非 dry-run で削除があった場合は `VACUUM` を実行

target ごとの policy:

- `events`: `events.created_at < cutoff` の row を削除します。紐づく `command_audits` は foreign key により cascade されます。
- `sessions`: `COALESCE(ended_at, started_at) < cutoff` かつ surviving event から参照されていない終了済み session を削除します。active session (`ended_at IS NULL`) は常に保護されます。
- `memories`: `updated_at < cutoff` の `expired` / `superseded` / `rejected` memory を物理削除します。`accepted` と `candidate` は age 削除しません。**例外:** 未レビューの auto-extracted candidate (`source IN (extracted, extracted-hidden, compact-summary)`) は 14 日超で **hard delete ではなく `expired` へ decay** し、keep-days の物理 GC まで restore 可能です（#1368）。物理削除時は evidence/artifact ref が cascade され、削除または decay 直前の行を指す `supersedes_memory_id` は先に NULL へ更新されます。
- `memory_edges`: `valid_to < cutoff` の終了済み edge を削除します。endpoint の memory が削除される場合も edge は自動 cascade されます。
- `all`: events、sessions、memories、memory_edges の順に依存関係を保って適用します。

実務上の意味:

- `gc` は opt-in であり、Traceary が background で自動削除することはありません
- 従来どおり event だけを掃除したい場合は `--target events` を使ってください
- 長期の監査履歴を残したい場合は、強めの cleanup の前に backup を取ってください
- cold 行の export と **verify-before-delete** は [Archive-before-GC](./archive-before-gc.ja.md)（#1309）を参照。フルファイル backup は [バックアップガイド](../backup/README.ja.md)

## 履歴 content の可逆的な dedupe

**要件。** 初期の hook 発火で、同じ prompt/transcript が二重に書き込まれることがありました。現在の hook 書き込みが抑止するのは、ホスト由来の安定した delivery ID で証明できる完全な再送だけです。その証拠がない同一内容は正当な別イベントとして保持します。履歴上の推定 duplicate group は残り、`doctor` の `content-event-reliability` 警告や context size を膨らませます。クリーンアップは **明示的かつ可逆** でなければなりません。通常の upgrade/migration が `events` 行を移動・削除・書き換えることは決してなく、復元可能な証跡なしに hard delete することもありません（#1227）。

**コマンド。** `traceary store dedupe content-events`

- 既定は **dry-run** で、候補グループを報告するだけで何も変更しません。
- `--apply` で duplicate を隔離します（`events` から移動）。
- `--restore <run-id>` で apply を取り消します。
- `--client codex`（既定）は Codex に限定し、`--client all` はすべての agent を対象にします。hook 由来の duplicate は `client=hook` で書かれるため、セレクタは `agent` で絞り込みます。
- `--strict` は時間差に関係なく完全一致する duplicate group をすべて報告します。
- `--json` は dry-run / apply / restore で利用できます。

**概念モデル。** duplicate group は identity tuple `kind, client, agent, session_id, workspace, source_hook, TrimSpace(body)` です。これは `content-event-reliability` 診断が使う identity と同じです（write-side guard は trimming なしの exact body を使うため異なります）。対象は `client='hook'` かつ `kind in (prompt, transcript)` の行のみで、**command audit は対象外** です。既定では、メンバーがほぼ同時（診断と同様に連続レコードをペアで cluster する 10s の近接 window 内）に書かれた group のみが対象になり、離れた意図的な再送は除外されます。`--strict` はこの window を外します。group ごとに残す **canonical** 行は、parse した `created_at` が最も早いもの（同値は event id が小さい方）です。`created_at` は Go 側で RFC3339Nano として parse します（`formatTimestamp` は可変幅の小数秒を出力するため、辞書順では並びません）。malformed な timestamp を含む group は **スキップして報告** し、変更しません。

**責務。** CLI（`presentation/cli/store_dedupe.go`）が flag を解析し text/JSON を整形します。usecase（`StoreManagementUsecase.DedupeContentEvents` / `RestoreContentEventDedupeRun`）が apply 時に run id と `archived_at` を採番し、入力を検証します。SQLite datasource（`StoreManagementDatasource`）が transaction 内での read/group/move と restore を担います。

**隔離テーブル。** migration `000019` が `event_content_dedupe_archive` を追加します（additive のみで `events` には触れません）。隔離された各行は、元の `events` 行をそのまま復元できる情報を保持します。`id, kind, client, agent, session_id, workspace, body`（正規化前の original）、`created_at, source_hook`、加えて来歴の `kept_event_id`（duplicate_of）、`dedupe_run_id`、`archived_at`、`group_key`、`reason` です。

**apply / restore のセマンティクス。**

- apply は単一 transaction（archive への insert ＋ `events` からの delete）で実行され、**冪等** です。2 回目の apply は、すでにクリーンアップ済みの group について `events` に duplicate が残っていないため、何も移動しません。
- restore は **all-or-nothing** で上書きを拒否します。元の event id がすでに `events` に存在する場合、restore 全体が失敗し何も変更しません。
- duplicate は `events` の *外* へ移動されるため、通常の `list`、`sessions --snapshot`、`doctor`、`context`、MCP の read surface からは自動的に見えなくなります。

**rollback。** apply を取り消すには `traceary store dedupe content-events --restore <run-id>` を実行します（run id は `--apply` が出力し、隔離した各行にも記録されます）。念のためのコピーが欲しい場合は、`--apply` の前に `traceary store backup create` を取得してください。

**振る舞いテスト。** dry-run の報告と非変更、apply ＋冪等性、restore ＋上書き拒否、malformed timestamp のスキップ、command-audit / 非 hook の除外、strict と近接スコープ、read surface の除外は `infrastructure/sqlite/content_event_dedupe_test.go` で、flag 配線と JSON/text 出力は `presentation/cli/store_dedupe_test.go` で、run id の採番は `application/usecase/store_dedupe_test.go` でカバーしています。

## backup の既定動作

サポートする backup 導線は意図的にシンプルです。

- `traceary store backup create` で compact な SQLite backup file を出力
- `traceary store backup restore` で destination DB path へその file をコピー
- restore 後に、現在の binary がより新しい schema version を知っていれば migration を再適用

マシン移行や破壊的 restore の注意点は専用ガイドを参照してください。
[`../backup/README.ja.md`](../backup/README.ja.md)

## 運用透明性のチェックリスト

ローカルで Traceary が何をしているか確認したいときは、次の順で見ると把握しやすいです。

1. `traceary doctor` で解決された DB path と書き込み可否を確認する
2. 正確な SQL が必要なら `schema/sqlite/migrations/` を見る
3. 手動調査や危険な cleanup の前に `traceary store backup create` を実行する
