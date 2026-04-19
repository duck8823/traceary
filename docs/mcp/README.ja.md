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

### Tool Search キーワードサマリ

Claude Code の MCP Tool Search はキーワードをもとにツール定義を遅延ロードします。以下の表は、各 Traceary ツールの用途・発見に使える主要キーワード・読み書きモード（破壊的書き込みかどうか）を一覧にしたものです。詳細な入力スキーマは表の後に続きます。

| Tool | 用途 | キーワード | モード |
|---|---|---|---|
| `add_log` | ログイベント、ノート、プロンプト、コンパクトサマリを追加する | log, note, prompt, compact summary | write (additive) |
| `start_session` | セッションを開始して `session_started` イベントを記録する | session, start, begin, workspace | write (additive) |
| `end_session` | セッションを終了して `session_ended` イベントを記録する | session, end, close, finish | write (additive) |
| `latest_session` | 再開・ハンドオフ向けに最新セッションを取得する | session, latest, resume, handoff | read |
| `active_session` | 再開向けにアクティブ/進行中のセッションを取得する | session, active, open, current | read |
| `list_events` | 最近のイベント、ログ、監査、プロンプト、サマリを一覧する | events, list, feed, timeline | read |
| `add_audit` | シェルコマンドの監査ログ（入出力はリダクト済）を追加する | audit, command, shell, exit code | write (additive) |
| `search` | テキスト・時刻・ワークスペースで過去履歴を検索する | search, find, query, history | read |
| `get_context` | セッションやワークスペース向けの最近のコンテキストを取得する | context, recent, session, workspace | read |
| `session_handoff` | 再開向けのセッションハンドオフサマリを取得する | handoff, resume, summary, working memory | read |
| `retrieve_memories` | ID、クエリ、ステータス、タイプ、スコープでメモリを取得する | memory, durable, retrieve, scope | read |
| `remember_memory` | 承認済みの永続メモリを記録する | memory, remember, accept, save | write (additive) |
| `propose_memory` | レビュー待ちの候補メモリを提案する | memory, propose, candidate, review | write (additive) |
| `accept_memory` | 候補メモリを承認する | memory, accept, approve, confidence | write (destructive) |
| `reject_memory` | 候補メモリを却下する | memory, reject, discard, review | write (destructive) |
| `supersede_memory` | 承認済みメモリを置き換えメモリで上書きする | memory, supersede, replace, update | write (destructive) |
| `expire_memory` | 永続メモリを期限切れ／退役にする | memory, expire, retire, forget | write (destructive) |
| `memory_pack` | プロンプトコンテキスト、ハンドオフ、自動化向けのメモリパックを構築する | memory pack, prompt context, handoff | read |

"write (destructive)" は既存の永続メモリ（候補または承認済み）の状態を変更するツールに付いています。Read-only ツールは SQLite を変更せず、additive write は新規イベントや新規メモリを作成するだけです。

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
- `limit`（既定: `20`）
- `offset`（既定: `0`）

`memory_id` を指定した場合は、evidence ref と artifact ref を含むその 1 件を返します。
`query` を指定した場合は全文検索を行います。
どちらも指定しない場合は、scope / status / type で必要に応じて絞り込みながら、active memory を一覧で返します。

Durable memory の payload は、保存済みの内容をそのまま返します。機微情報は、永続化前に既存の memory sanitization / redaction 経路で処理されている前提です。

### `remember_memory`

accepted 状態の durable memory を追加します。

Inputs:

- `type`（必須）
- `workspace` / `agent` / `session_family` のいずれか 1 つ（必須、相互排他）
- `fact`（必須）
- `confidence`
- `source`
- `evidence_refs`
- `artifact_refs`

accepted memory には、少なくとも 1 つの evidence ref が必要です。

### `propose_memory`

レビュー前の candidate durable memory を追加します。

Inputs:

- `type`（必須）
- `workspace` / `agent` / `session_family` のいずれか 1 つ（必須、相互排他）
- `fact`（必須）
- `source`
- `evidence_refs`
- `artifact_refs`

### `accept_memory`

candidate durable memory を accepted に更新します。

Inputs:

- `memory_id`（必須）
- `confidence`

### `reject_memory`

candidate durable memory を rejected に更新します。

Inputs:

- `memory_id`（必須）

### `supersede_memory`

accepted durable memory を superseded にし、新しい accepted memory を保存します。

Inputs:

- `memory_id`（必須）
- `fact`（必須）
- 置き換え後の `type`
- 置き換え後の `workspace` / `agent` / `session_family`
- `confidence`
- `source`
- `evidence_refs`
- `artifact_refs`

置き換え後の `type` や scope を省略した場合は、現在の memory の値を引き継ぎます。

### `expire_memory`

memory を expired に更新します。

Inputs:

- `memory_id`（必須）
- `expires_at`（`YYYY-MM-DD` または RFC3339。省略時は現在時刻）

### `memory_pack`

working memory と durable memory を合わせた、プロンプトへの文脈補完や自動化向けの context pack を返します。

Inputs:

- `workspace`
- `session_id`
- `recent_commands_limit`（既定: `5`。明示的に `0` を渡すと recent commands を無効化）
- `memory_limit`（既定: `5`。明示的に `0` を渡すと durable memories を無効化）

## 実用的なクライアント設定例

多くの stdio MCP クライアントでは、`mcpServers` を次のように設定できます。

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

クライアントごとの設定形式が違っても、次の 3 点は共通です。

- command: `traceary`
- args: `["mcp-server"]`
- optional env: `TRACEARY_DB_PATH=/path/to/traceary.db`

## 推奨ワークフロー

実運用では、次の流れが扱いやすいです。

1. hooks で session 境界と command audit を自動記録する
2. 同じ Traceary DB を MCP からも参照できるようにする
3. セッションを明示的に再開したいときは `active_session` または `latest_session` を呼ぶ
4. 条件なしで最近の流れを見たいときは `list_events` を呼ぶ
5. 新しい作業に入る前に `get_context` を呼ぶ
6. 古いコマンド出力やメモを探したいときは `search` を呼ぶ
7. クライアント自身が session lifecycle を管理したい場合だけ `start_session` / `end_session` / `add_log` / `add_audit` を使う

こうしておくと、受動的な取り込みと能動的な文脈取得を同じローカルストアで扱えます。

## 関連ドキュメント

- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- リリース / インストールガイド: [`../release/README.ja.md`](../release/README.ja.md)
- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
