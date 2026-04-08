# CLI リファレンス

[English](./README.md)

このページは、Traceary の公開 CLI surface の安定したリファレンスです。
最初の導入には `README.ja.md` の quick start と合わせて使ってください。

## 共通ルール

- DB path の解決順: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- 更新系コマンドは既定で人間向けテキストを出力します
- event / session の識別子を返すコマンドは、script 向けに `--id-only` をサポートします
- structured output があるコマンドは `--json` をサポートします

## イベント記録コマンド

### `traceary log <message>`

note event を追記します。

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

session 解決ルール:

- 明示 `--session-id` または `TRACEARY_SESSION_ID` を最優先
- それ以外では、解決できた repo / work context に対する最新の non-stale active session を再利用
- repo / work context が無い、または一致する active session が無い場合は、従来どおり `default` session ID に fallback

### `traceary audit [<command> <input> <output>]`

command execution audit event を記録します。

入力方法:

- 位置引数: `traceary audit "go test ./..." '{}' '{}'`
- named flags: `traceary audit --command "go test ./..." --input '{}' --output '{}'`

主な flag:

- `--command`
- `--input`
- `--output`
- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

session 解決ルールは `traceary log` と同じです。

## 参照・検索コマンド

### `traceary list`

最近の event を一覧表示します。

主な flag:

- `--limit`
- `--offset`
- `--json`
- `--client`
- `--agent`
- `--repo`
- `--session-id`

### `traceary search [<query>]`

全文検索と構造フィルタで event を検索します。

主な flag:

- `--kind`
- `--client`
- `--agent`
- `--repo`
- `--session-id`
- `--since`
- `--until`
- `--limit`
- `--offset`
- `--json`

### `traceary show <event-id>`

1 件の event を詳細表示します。

主な flag:

- `--json`

### `traceary context`

別 session / 別 tool へ渡すための handoff/context bundle を表示します。

alias:

- `traceary handoff`

主な flag:

- `--session-id`
- `--repo`
- `--limit`
- `--json`

## Session コマンド

### `traceary session start`

session start 境界を記録し、session ID を出力します。

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

### `traceary session end`

session end 境界を記録し、生成された event ID を出力します。

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--repo`
- `--id-only`

### `traceary session latest`

条件に一致する最新 session ID を表示します。

主な flag:

- `--client`
- `--agent`
- `--repo`

### `traceary session active`

条件に一致する active session ID を表示します。

主な flag:

- `--client`
- `--agent`
- `--repo`
- `--stale-after`
- `--allow-stale`

## Hooks と診断

### `traceary hooks print`

対応 client 向けの generated hook config を出力します。

主な flag:

- `--client`
- `--traceary-bin`

### `traceary hooks install`

対応 client の標準 config path に generated hook config を書き出します。

主な flag:

- `--client`
- `--project-dir`
- `--traceary-bin`
- `--output`
- `--force`

### `traceary hooks guide`

対応 client ごとの install / check / verify 手順を出力します。

主な flag:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

DB access、hook script、client config 統合状態を診断します。

alias:

- `traceary status`

主な flag:

- `--client`
- `--project-dir`
- `--json`

## Backup と maintenance

### `traceary init`

DB 作成と migration 適用を明示的に先行実行します。
通常コマンドでも on-demand 初期化されるため、必須ではありません。

### `traceary backup create`

compact な SQLite backup file を作成します。

主な flag:

- `--output`
- `--db-path`
- `--force`

### `traceary backup restore`

backup file から DB を復元します。

主な flag:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary gc`

古い event を削除し、必要に応じて SQLite store を compact します。

主な flag:

- `--before`
- `--keep-days`
- `--vacuum`
- `--dry-run`

## Integration コマンド

### `traceary mcp-server`

AI client 統合向けに MCP server を stdio で起動します。

## 関連 docs

- onboarding / quick start: [`../../README.ja.md`](../../README.ja.md)
- 環境変数と runtime 前提: [`../environment/README.ja.md`](../environment/README.ja.md)
- hooks integration: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- backup flow: [`../backup/README.ja.md`](../backup/README.ja.md)
