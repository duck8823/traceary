# ストレージモデル

[English](./README.md)

Traceary は、ローカル状態を 1 つの SQLite DB file に保存します。
このガイドでは、何がどこに保存されるのか、schema がどう構成されているのか、現在の `gc` / backup の既定動作が何かを説明します。

## local-first な配置

- 既定の DB path: `~/.config/traceary/traceary.db`
- 上書き方法: `--db-path` または `TRACEARY_DB_PATH`
- file permission: parent directory は `0700`、DB file は `0600` で作成
- hidden remote service はなし: CLI / hooks / MCP server は同じローカル SQLite file を読み書きします

`traceary init` は任意です。ストアが必要なコマンドは、必要に応じて DB を作成し、migration を自動適用します。

## 現在の schema

現在の Traceary は次の table を作成します。

### `events`

append-only な event stream です。note、session 境界、review record、command audit はすべてここから始まります。

column:

- `id`: event identifier
- `kind`: `note`、`command_executed`、`session_started`、`session_ended` などの event kind
- `agent`: `codex`、`claude`、`gemini`、`manual` などの logical actor
- `session_id`: session grouping identifier
- `body`: event の人間向けメッセージ
- `created_at`: RFC3339 timestamp
- `client`: `cli`、`claude`、`codex`、`gemini` などの ingestion path
- `repo`: 利用可能な場合の current repository / workspace identifier

主な index:

- `idx_events_session_created_at` on `(session_id, created_at)`
- `idx_events_created_at` on `(created_at DESC, id DESC)`

### `command_audits`

`command_executed` event に紐づく構造化 audit detail です。

column:

- `event_id`: primary key かつ `events.id` への foreign key
- `command_text`: 記録した command line
- `input_text`: 保存した command input payload
- `output_text`: 保存した command output payload
- `input_truncated`: input を切り詰めたかどうか
- `output_truncated`: output を切り詰めたかどうか

`command_audits.event_id` は `ON DELETE CASCADE` なので、`gc` によって event を削除すると対応する audit payload も同時に消えます。

## Traceary が保存しないもの

現在の non-goals:

- SQLite file 以外に置く background daemon metadata
- hidden な cloud sync や hosted history service
- line-oriented export format を主 persistence layer にすること
- `schema/sqlite/migrations` に埋め込まれた SQL 以外の migration registry

## migration と互換性

- migration は `schema/sqlite/migrations` から binary に埋め込みます
- 通常コマンドの実行前に store initialization が走るため、upgrade 時も migration は自動適用されます
- backup restore は、まず SQLite file をコピーし、その後に store initialization を再実行して newer migration を適用します

任意の手動 schema edit との後方互換は保証しません。
持ち運べるコピーが必要な場合は、DB を直接編集する代わりに `traceary backup create` を使ってください。

## `gc` の既定動作

`traceary gc` は古い event を retention ベースで掃除するコマンドです。

- 既定 retention: `90` 日 (`--keep-days 90`)
- 選択条件: `created_at < cutoff` の row を削除
- `--dry-run`: 削除候補件数だけ表示
- 削除後に `VACUUM` を実行

実務上の意味:

- `gc` は opt-in であり、Traceary が background で自動削除することはありません
- `gc` を実行すると、該当 event と紐づく `command_audits` も削除されます
- 長期履歴を残したい場合は、強めの cleanup の前に backup を取ってください

## backup の既定動作

サポートする backup 導線は意図的にシンプルです。

- `traceary backup create` で compact な SQLite backup file を出力
- `traceary backup restore` で destination DB path へその file をコピー
- restore 後に、現在の binary がより新しい schema version を知っていれば migration を再適用

マシン移行や破壊的 restore の注意点は専用ガイドを参照してください:
[`../backup/README.ja.md`](../backup/README.ja.md)

## 運用透明性のチェックリスト

ローカルで Traceary が何をしているか確認したいときは:

1. `traceary doctor` で解決された DB path と書き込み可否を確認する
2. 正確な SQL が必要なら `schema/sqlite/migrations/` を見る
3. 手動調査や risky cleanup の前に `traceary backup create` を実行する

