# MCP integration

[English](./README.md)

Traceary は、ローカル SQLite 履歴を stdio MCP server として公開できます。
別の AI client から、CLI を直接 shell 実行する代わりに MCP tool 経由で Traceary の読み書きをさせたいときに使います。

## どの統合経路を使うべきか

用途に対して最も単純な経路を選んでください。

| Need | Best path |
| --- | --- |
| shell script や手動操作から記録・参照したい | direct CLI (`traceary log`, `traceary audit`, `traceary search`, ...) |
| Claude Code / Codex CLI / Gemini CLI から session 境界や shell command audit を自動取り込みしたい | hooks |
| MCP 対応 client から過去文脈を検索したり、tool 経由でイベントを書き込みたい | `traceary mcp-server` |

hooks と MCP は競合ではなく補完関係です。

- hooks は session start / end や shell audit の受動的な取り込みに向いています
- MCP は `search` / `get_context` や明示的な session tool を client が能動的に呼びたいときに向いています

## 対応プラットフォーム

- `traceary mcp-server` は core CLI と同じ support promise に従い、macOS / Linux で継続的に検証しています
- 事前ビルド済み binary は macOS / Linux 向けに公開し、他の Go 対応 Unix 系環境は `go install` で動く可能性があります
- standalone の MCP server 自体には `bash` は不要ですが、hooks 連携には引き続き `bash` が必要です
- Windows native の正式対応はまだ約束していないため、必要な場合は WSL などの POSIX 互換環境を使ってください

## server の起動

MCP server は stdio を使います。
network port は開きません。

```sh
traceary mcp-server
```

既定以外の SQLite file を使いたい場合は、`TRACEARY_DB_PATH` または `--db-path` を使います。

```sh
TRACEARY_DB_PATH=/path/to/traceary.db traceary mcp-server
traceary mcp-server --db-path /path/to/traceary.db
```

DB path は次の順で解決されます。

1. `--db-path`
2. `TRACEARY_DB_PATH`
3. `~/.config/traceary/traceary.db`

現時点の `traceary mcp-server --help` は次のとおりです。

```text
Run the Traceary MCP server over stdio

Usage:
  traceary mcp-server [flags]

Flags:
      --db-path string   SQLite DB path (env: TRACEARY_DB_PATH)
  -h, --help             help for mcp-server
```

## 公開される tools

現在の Traceary MCP server は 9 つの tool を公開します。

### `start_session`

`session_started` イベントを記録します。

Inputs:

- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（任意。省略時は Traceary が生成）
- `repo`（任意の work-context 文字列）

### `end_session`

`session_ended` イベントを記録します。

Inputs:

- `session_id`（必須）
- `client`（任意。省略時は対応する `session_started` から attribution を優先）
- `agent`（任意。省略時は対応する `session_started` から attribution を優先）
- `repo`（任意の work-context 文字列）

### `latest_session`

条件に一致する最新 session を返します。

Inputs:

- `client`
- `agent`
- `repo`

### `active_session`

条件に一致する最新の active session を返します。

Inputs:

- `client`
- `agent`
- `repo`
- `allow_stale`（既定: `false`）
- `stale_after_seconds`（`0` または省略時は既定の `86400`）

### `list_events`

新しい順で最近の event 一覧を返します。

Inputs:

- `limit`（既定: `20`）
- `offset`（既定: `0`）

client から `traceary list` 相当の「最近の feed」を見たいときは `list_events` を使います。
`repo` や `session_id`、全文検索などの structured filter が必要なときは `search` を使ってください。

### `add_log`

note 系イベントを記録します。

Inputs:

- `message`（必須）
- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（既定: `default`）
- `repo`（任意の work-context 文字列）

### `add_audit`

shell command audit イベントを記録します。

CLI と同様に、`add_audit` も SQLite へ書き込む前に一般的な secret っぽい値を伏せ字化します。これは完全保証ではなく、best-effort の保護です。MCP surface では意図的に `allow-secrets` 相当の override は提供していません。raw payload を残したい場合だけ direct CLI を使ってください。

Inputs:

- `command`（必須）
- `input`
- `output`
- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（既定: `default`）
- `repo`（任意の work-context 文字列）

### `search`

既存イベントを検索します。

Inputs:

- `query`（必須）
- `repo`
- `from`（`YYYY-MM-DD` または RFC3339）
- `to`（`YYYY-MM-DD` または RFC3339）
- `limit`（既定: `20`）

### `get_context`

handoff 用の最近イベント群を返します。

Inputs:

- `repo`
- `session_id`
- `limit`（既定: `20`）

## 実用的な client 設定例

stdio MCP client の多くは、次のような `mcpServers` entry を受け取れます。

```json
{
  "mcpServers": {
    "traceary": {
      "command": "traceary",
      "args": ["mcp-server"],
      "env": {
        "TRACEARY_DB_PATH": "/Users/you/.config/traceary/traceary.db"
      }
    }
  }
}
```

もし client 側の config 形状が異なる場合でも、次の 3 要素をその client の形式に読み替えれば足ります。

- command: `traceary`
- args: `["mcp-server"]`
- optional env: `TRACEARY_DB_PATH=/path/to/traceary.db`

## 推奨ワークフロー

実用上は次の組み合わせが扱いやすいです。

1. hooks で session 境界と command audit を自動記録する
2. 同じ Traceary DB を MCP でも接続する
3. 明示的に session を再開したいときは `active_session` または `latest_session` を呼ぶ
4. filter 無しの最近 feed が欲しいときは `list_events` を呼ぶ
5. 新しい task の前に client から `get_context` を呼ぶ
6. 過去の command output や note が必要になったら `search` を呼ぶ
7. client 側で session lifecycle まで管理したい場合は `start_session` / `end_session` / `add_log` / `add_audit` を使う

これで、受動的な取り込みと能動的な文脈検索をひとつの local store にまとめられます。

## 関連文書

- hooks 取り込みガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- release / install ガイド: [`../release/README.ja.md`](../release/README.ja.md)
