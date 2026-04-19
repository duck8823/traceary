# CLI リファレンス

[English](./README.md)

このページでは、公開 CLI の挙動をコマンド別にまとめています。
導入直後は `README.ja.md` のクイックスタートと合わせて参照してください。

## 共通ルール

- DB path の解決順: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- 更新系コマンドは既定で読みやすいテキスト形式を出力します
- イベント / セッションの識別子を返すコマンドは、スクリプト向けに `--id-only` をサポートします
- 構造化出力を持つコマンドは `--json` をサポートします

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

デフォルトのテキスト出力は `tail` と同じコンパクトな 1 行形式 (`HH:MM:SS  kind  sess=<先頭8文字>  ws=<basename>  message`、ヘッダ無し、現地時刻) です。`--wide` で従来の 7 カラム tab 区切り表、`--utc` でテキスト出力を UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の出力を完全再現します。`--json` は従来通りです。`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > `~/.config/traceary/config.json` の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールド: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`。

主な flag:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`

### `traceary tail`

新しい event を追跡表示します。

`tail` はイベントの流れをその場で追いかけるためのコマンドです。最初に最近の backlog を表示し、その後はローカルストアに追加される一致 event を継続して追跡します。hook が正しく動いているか、想定した session / workspace に書き込まれているか、失敗がリアルタイムで見えているかを確認したいときに向いています。`list` のように 1 回で終わらず、`search` のようなキーワード検索も行いません。`handoff` と違って working memory は組み立てず、生の event stream をそのまま表示します。

デフォルトのテキスト出力は `HH:MM:SS  kind  sess=<先頭8文字>  ws=<basename>  message` というコンパクトな 1 行形式で、約 100 カラムに収まり、タイムスタンプは現地時刻 (local time) を使用します。`--wide` で従来の 7 カラム tab 区切り形式、`--utc` でテキスト出力のタイムスタンプを UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前と完全に同一のバイト列を再現するので、既存スクリプトとの互換を保てます。`--json` を付けると改行区切り JSON（1 行 1 event）を出力し、パイプラインから逐次処理できます（JSON の時刻は RFC3339 のままで `--utc` の影響を受けません）。

> コンパクト表示の session ID (`sess=<先頭8文字>`) は人間が目視する前提の短縮形です。機械処理には `--wide --utc` または `--json` を利用してください。

`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > config.json の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールドは `traceary list` の説明を参照してください。

主な flag:

- `--kind`
- `--limit`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--failures`

### `traceary search [<query>]`

全文検索と構造フィルタで event を検索します。

テキスト出力は `list` / `tail` と同じコンパクト 1 行形式 (デフォルトで現地時刻) です。`--wide` で従来の 7 カラム表、`--utc` で UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の出力を完全再現します。`--json` は従来通りです。`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > config.json の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールドは `traceary list` の説明を参照してください。

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

### `traceary timeline`

ギャップ検出による作業タイムラインを、ワークスペース単位のアクティビティ要約付きで表示します。

`timeline` は直近のイベントをアイドルギャップ（デフォルト 15 分）で区切って連続する作業ブロックに分け、各ブロック内で workspace ごとに整列された 1 行を表示します。ワークスペース単位のアクティビティ要約は **`compact_summary` → 最初の `prompt` → kind counts** のフォールバック順で選ばれ、そのブロック内でそのワークスペースに存在するシグナルが 1 行に展開されます。デフォルトのテキスト出力は現地時刻 (local time) で、`--utc` で UTC に切り替えられます。`--json` はブロックスキーマに `workspace_breakdown` 配列 (`{workspace, event_count, kind_counts, summary, summary_source}`) を追加します（既存フィールドは維持され後方互換）。

主な flag:

- `--workspace`
- `--from`
- `--to`
- `--gap` (アイドルギャップ閾値/分)
- `--limit`
- `--json`
- `--utc`

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

### `traceary handoff`

session metadata、recent commands、compact summary、accepted durable memories から組み立てた handoff summary を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary session handoff`

`traceary handoff` と同じ handoff 出力を session namespace から実行します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`

### `traceary compact-summary`

`traceary handoff` と同じ working-memory pack を使い、prompt injection や compact/clear 後の再開に向けた短い要約を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`

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

### `traceary memory extract`

対象 session の session summary、compact summary、prompt event、note / review signal から candidate durable memory を抽出します。抽出結果は candidate のみで、Traceary が自動で accept することはありません。prompt event は任意で、prompt や compact-summary event が無い場合も、利用できる signal の範囲で動作します。`--session-id` を省略した場合は、まず active session を解決し、見つからなければ workspace 内の latest session を使います。`Feedback:` / `Correction:` ラベルは、現在の最小 durable-memory taxonomy では `preference` candidate として保持されます。保存される candidate は、他の durable memory と同じ sanitization / redaction 経路を通ってから永続化されます。

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

DB アクセス、生成済み hook 設定の有無、クライアント設定のつながりを診断します。

`warn` は、hooks 未導入などの初回状態や未設定状態を表します。
`fail` は、DB アクセス不良や unreadable / invalid config のような壊れた状態を表します。
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
通常コマンドでも必要に応じて初期化されるため、必須ではありません。

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
