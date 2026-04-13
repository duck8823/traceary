# MCP ガイド

[English](./README.md)

Traceary は、ローカル SQLite の履歴を stdio で動く MCP サーバーとして公開できます。
別の AI クライアントから CLI を直接呼ぶ代わりに、MCP ツールとして Traceary を読み書きさせたいときに使います。

## どの経路を使うべきか

用途ごとに、いちばん単純な経路を選んでください。

| 用途 | 向いている経路 |
| --- | --- |
| shell script や手動操作から記録・参照したい | CLI を直接使う（`traceary log`, `traceary audit`, `traceary search`, ...） |
| Claude Code / Codex CLI / Gemini CLI から session 境界や shell command audit を自動で取り込みたい | hooks |
| MCP 対応 client から過去の文脈を検索したり、tool 経由でイベントを書き込みたい | `traceary mcp-server` |

hooks と MCP は競合するものではなく、役割が違います。

- hooks は session start / end や shell audit を自動で受け取るのに向いています
- MCP は `search` / `get_context` や session 系 tool を client が明示的に呼びたいときに向いています

## 対応プラットフォーム

- `traceary mcp-server` は CLI 本体と同じ方針で、macOS / Linux で継続的に検証しています
- 事前ビルド済みバイナリは macOS / Linux 向けに公開し、他の Go 対応 Unix 系環境は `go install` で動く可能性があります
- MCP server 単体を動かすだけなら `bash` は不要ですが、hooks 連携には引き続き `bash` が必要です
- Windows native の正式対応はまだ約束していないため、必要な場合は WSL などの POSIX 互換環境を使ってください

## サーバーの起動

MCP サーバーは stdio で動作します。ネットワークポートは開きません。

```sh
traceary mcp-server
```

既定以外の SQLite ファイルを使いたい場合は、`TRACEARY_DB_PATH` または `--db-path` を使います。

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

## 公開されるツール

現在の Traceary MCP サーバーは 18 個のツールを公開します。

### `start_session`

`session_started` イベントを記録します。

Inputs:

- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（任意。省略時は Traceary が生成）
- `workspace`（任意の work-context 文字列）

### `end_session`

`session_ended` イベントを記録します。

Inputs:

- `session_id`（必須）
- `client`（任意。省略時は対応する `session_started` から attribution を優先）
- `agent`（任意。省略時は対応する `session_started` から attribution を優先）
- `workspace`（任意の work-context 文字列）

### `latest_session`

条件に一致する最新 session を返します。

Inputs:

- `client`
- `agent`
- `workspace`

### `active_session`

条件に一致する最新の active session を返します。

Inputs:

- `client`
- `agent`
- `workspace`
- `allow_stale`（既定: `false`）
- `stale_after_seconds`（`0` または省略時は既定の `86400`）

### `list_events`

新しい順で最近の event 一覧を返します。

Inputs:

- `limit`（既定: `20`）
- `offset`（既定: `0`）

client 側で `traceary list` 相当の「最近の feed」を見たいときは `list_events` を使います。`workspace` や `session_id`、全文検索などの絞り込みが必要なときは `search` を使ってください。

### `add_log`

note 系イベントを記録します。

Inputs:

- `message`（必須）
- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（既定: `default`）
- `workspace`（任意の work-context 文字列）

### `add_audit`

shell command audit イベントを記録します。

CLI と同様に、`add_audit` も SQLite に書き込む前に一般的な secret らしい値を伏せ字化します。これは完全保証ではなく、ベストエフォートの保護です。MCP では意図的に `allow-secrets` 相当の override を提供していません。raw payload を残したい場合だけ direct CLI を使ってください。

Inputs:

- `command`（必須）
- `input`
- `output`
- `client`（既定: `mcp`）
- `agent`（既定: `manual`）
- `session_id`（既定: `default`）
- `workspace`（任意の work-context 文字列）

### `search`

既存イベントを検索します。

Inputs:

- `query`（必須）
- `workspace`
- `from`（`YYYY-MM-DD` または RFC3339）
- `to`（`YYYY-MM-DD` または RFC3339）
- `limit`（既定: `20`）

### `get_context`

直近の生イベント列を返します。

Inputs:

- `workspace`
- `session_id`
- `limit`（既定: `20`）

### `session_handoff`

CLI の `traceary handoff` とそろえた、構造化された working-memory handoff pack を返します。

トップレベルの `summary` は後方互換のために残してあり、`working_state.combined_summary` をそのまま返します。

Inputs:

- `workspace`
- `session_id`
- `recent_commands_limit`（既定: `5`。明示的に `0` を渡すと recent commands を無効化）
- `memory_limit`（既定: `5`。明示的に `0` を渡すと durable memories を無効化）

### `retrieve_memories`

ID 指定、全文検索、scope filter で durable memory を取得します。

Inputs:

- `memory_id`
- `query`
- `workspace`
- `agent`
- `session_family`
- `status`
- `type`

### `propose_memory`

candidate 状態の durable memory を追加します。

Inputs:

- `fact`（必須）
- `type`（必須）
- `workspace` / `agent` / `session_family`（いずれか 1 つ以上）
- `evidence_refs`
- `artifact_refs`

### `remember_memory`

accepted 状態の durable memory を直接追加します。

Inputs:

- `fact`（必須）
- `type`（必須）
- `workspace` / `agent` / `session_family`（いずれか 1 つ以上）
- `evidence_refs`（必須）
- `artifact_refs`
- `confidence`（既定: `verified`）

### `accept_memory`

candidate memory を accepted に更新します。

Inputs:

- `memory_id`（必須）
- `confidence`（既定: `verified`）

### `reject_memory`

candidate memory を rejected に更新します。

Inputs:

- `memory_id`（必須）

### `expire_memory`

memory を expired に更新します。

Inputs:

- `memory_id`（必須）
- `expires_at`（省略時は現在時刻）

### `supersede_memory`

accepted memory を新しい accepted memory で置き換えます。

Inputs:

- `memory_id`（必須）
- `fact`（必須）
- `evidence_refs`（必須）
- `artifact_refs`
- `confidence`（既定: `verified`）

### `memory_pack`

working memory と durable memory を合わせた、プロンプト注入向けの context pack を返します。

Inputs:

- `workspace`
- `session_id`
- `recent_commands_limit`（既定: `5`。明示的に `0` を渡すと recent commands を無効化）
- `memory_limit`（既定: `5`。明示的に `0` を渡すと durable memories を無効化）

## 使い分けの目安

- まず事実を残すだけなら `add_log` / `add_audit`
- 直近の流れをざっと見たいなら `list_events` / `get_context`
- セッション再開用の要約がほしいなら `session_handoff`
- あとで再利用したい判断や制約を残したいなら `propose_memory` / `remember_memory`
- durable memory を含む prompt 用パックがほしいなら `memory_pack`

## よくある運用パターン

1. hooks で session 境界と command audit を自動記録する
2. 必要に応じて MCP から `search` や `get_context` を呼ぶ
3. セッションをまたいで残したい知識だけ `propose_memory` / `remember_memory` で durable memory に昇格する
4. 次のエージェントへ渡すときは `session_handoff` または `memory_pack` を使う

## 参考

- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- イベントライフサイクル: [`../lifecycle.ja.md`](../lifecycle.ja.md)
- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
