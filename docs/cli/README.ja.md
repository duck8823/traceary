# CLI リファレンス

[English](./README.md)

このページは、Traceary の公開 CLI の挙動をまとめたリファレンスです。
導入時は `README.ja.md` のクイックスタートと合わせて参照してください。

## 共通ルール

- DB path の解決順: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- 更新系コマンドは既定で人間向けテキストを出力します
- イベント / セッションの識別子を返すコマンドは、スクリプト向けに `--id-only` をサポートします
- 構造化出力を持つコマンドは `--json` をサポートします

## イベント記録コマンド

### `traceary log <message>`

note event を追記します。

既定値:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / 検出した workspace
- `--session-id`: flag → `TRACEARY_SESSION_ID` → 解決した repo の最新 non-stale active session → `default`

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`

session 解決ルール:

- 明示 `--session-id` または `TRACEARY_SESSION_ID` を最優先
- それ以外では、解決できた repo / work context に対する最新の non-stale active session を再利用
- `remote.origin.url` が無くても Git worktree 内であれば、work-context key として worktree ルートパスを使います
- repo / work context が無い、または一致する active session が無い場合は、従来どおり `default` session ID に fallback

> **注意:** `log` と `audit` は `--session-id` の値を検証せずにそのまま受け入れます。これは設計上の意図です — hook は高頻度でイベントを記録するため、書き込みごとに DB ルックアップを追加するとオーバーヘッドが無視できません。存在しないセッション ID を渡してもイベントは記録されますが、セッション単位のクエリには表示されません。

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

`list` は直近履歴を素早く絞るためのコマンドです。kind / client / agent / session / repo が決まっているときはこちらを使い、キーワード検索や期間条件が必要なときは `search` を使います。

主な flag:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`

### `traceary search [<query>]`

全文検索と構造フィルタで event を検索します。

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

### `traceary show <event-id>`

1 件の event を詳細表示します。

主な flag:

- `--json`

### `traceary context`

別 session / 別 tool へ渡すための raw recent context event 群を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--limit`
- `--json`

### `traceary handoff`

session metadata、recent commands、compact summary、accepted durable memories から構成した structured working-memory handoff summary を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary session handoff`

`traceary handoff` と同じ structured handoff 出力を session namespace から実行します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary compact-summary`

`traceary handoff` と同じ working-memory pack を使って、prompt injection や compact/clear 後の再開向けに圧縮した context-resumption pointer を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`

## Durable memory コマンド

### `traceary memory list`

Durable memory を一覧表示します。scope flag を明示しない場合、`memory list` は解決した workspace scope を既定で使います。

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

全文検索と構造フィルタで durable memory を検索します。query または filter のいずれか 1 つ以上が必要です。

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

candidate な durable memory を記録します。後で review できます。

主な flag は `memory remember` と同じですが、`--confidence` は使われません。

### `traceary memory extract`

対象 session の session summary、compact summary、prompt event、note/review signal から candidate durable memory を抽出します。抽出結果は candidate のみで、Traceary が auto-accept することはありません。prompt event は任意で、prompt や compact-summary event がなくても利用可能な signal の範囲で劣化動作します。`--session-id` を省略した場合は、まず active session を解決し、見つからなければ workspace 内の latest session にフォールバックします。`Feedback:` / `Correction:` ラベルは、現在の最小 durable-memory taxonomy の中で `preference` candidate として保持されます。保存される candidate は、他の durable memory 書き込みと同じ sanitization/redaction 経路を通ってから永続化されます。

主な flag:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
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
- `--force`

### `traceary hooks guide`

対応クライアントごとの install / check / verify 手順を出力します。

主な flag:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

DB アクセス、hook スクリプト、クライアント設定の統合状態を診断します。

`warn` は、hooks 未導入などの first-run / 未設定状態です。
`fail` は、DB アクセス不良や unreadable / invalid config のような壊れた状態です。
`traceary doctor` が非 0 で終了するのは `fail` があるときだけです。

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

コンパクトな SQLite バックアップファイルを作成します。

主な flag:

- `--output`
- `--db-path`
- `--force`

### `traceary backup restore`

バックアップファイルから DB を復元します。

主な flag:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary gc`

古いイベントを削除し、必要に応じて SQLite ストアを圧縮します。

主な flag:

- `--before`
- `--keep-days`
- `--vacuum`
- `--dry-run`

## Integration コマンド

### `traceary mcp-server`

AI クライアント統合向けに MCP サーバーを stdio で起動します。

## 関連ドキュメント

- オンボーディング / クイックスタート: [`../../README.ja.md`](../../README.ja.md)
- 環境変数と runtime 前提: [`../environment/README.ja.md`](../environment/README.ja.md)
- hooks integration: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- backup flow: [`../backup/README.ja.md`](../backup/README.ja.md)
