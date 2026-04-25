# ストレージモデル

[English](./README.md)

Traceary は、ローカル状態を 1 つの SQLite DB ファイルに保存します。
このガイドでは、何がどこに保存されるのか、現在の schema がどう構成されているのか、`gc` / backup の既定動作が実際に何を意味するのかを整理します。

## ローカルファーストの配置

- 既定の DB path: `~/.config/traceary/traceary.db`
- 上書き方法: `--db-path` または `TRACEARY_DB_PATH`
- file permission: parent directory は `0700`、DB file は `0600` で作成
- 外部のホスト型サービスは使わない: CLI / hooks / MCP server は同じローカル SQLite ファイルを読み書きする

`traceary init` は任意です。ストアが必要なコマンドは、必要に応じて DB を作成し、migration を自動適用します。

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
- `exit_code`: 取得できた場合の終了コード

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

任意の手動 schema edit との後方互換は保証しません。持ち運べるコピーが必要な場合は、DB を直接編集する代わりに `traceary backup create` を使ってください。

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
- `memories`: `updated_at < cutoff` の `expired` / `superseded` memory だけを削除します。`accepted`、`proposed`、その他の status は status が変わらない限り無期限に保持します。evidence/artifact ref は cascade され、削除対象を指す `supersedes_memory_id` は FK 整合性維持のため事前に NULL へ更新されます。
- `memory_edges`: `valid_to < cutoff` の終了済み edge を削除します。endpoint の memory が削除される場合も edge は自動 cascade されます。
- `all`: events、sessions、memories、memory_edges の順に依存関係を保って適用します。

実務上の意味:

- `gc` は opt-in であり、Traceary が background で自動削除することはありません
- 従来どおり event だけを掃除したい場合は `--target events` を使ってください
- 長期の監査履歴を残したい場合は、強めの cleanup の前に backup を取ってください

## backup の既定動作

サポートする backup 導線は意図的にシンプルです。

- `traceary backup create` で compact な SQLite backup file を出力
- `traceary backup restore` で destination DB path へその file をコピー
- restore 後に、現在の binary がより新しい schema version を知っていれば migration を再適用

マシン移行や破壊的 restore の注意点は専用ガイドを参照してください。
[`../backup/README.ja.md`](../backup/README.ja.md)

## 運用透明性のチェックリスト

ローカルで Traceary が何をしているか確認したいときは、次の順で見ると把握しやすいです。

1. `traceary doctor` で解決された DB path と書き込み可否を確認する
2. 正確な SQL が必要なら `schema/sqlite/migrations/` を見る
3. 手動調査や危険な cleanup の前に `traceary backup create` を実行する
