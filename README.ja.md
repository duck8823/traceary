# Traceary

[English](./README.md)

[Changelog](./CHANGELOG.ja.md)

[文書ガイド](./docs/README.ja.md)

[コントリビューション](./CONTRIBUTING.ja.md)

[セキュリティポリシー](./SECURITY.ja.md)

[MCP ガイド](./docs/mcp/README.ja.md)

[バックアップガイド](./docs/backup/README.ja.md)

[リリースガイド](./docs/release/README.ja.md)

[CLI リファレンス](./docs/cli/README.ja.md)

[環境変数リファレンス](./docs/environment/README.ja.md)

Traceary は、AI エージェントの作業ログと audit log をローカルに記録・検索する local-first な CLI / MCP サーバーです。

## 背景と課題

AI エージェントを日常的に使うと、次のような問題が起きます。

- `clear` / `compact` でセッション文脈が失われやすい
- Git 履歴では「何をしたか」は追えても、「なぜそうしたか」は残りにくい
- どのエージェントがどのコマンドを実行したかを確認しづらい
- Claude / Codex / Gemini 間で文脈が分断されやすい
- 並列セッションや worktree 移動で履歴が追いにくくなる
- ログが増え続けるため、保持期間や `gc` が必要になる

Traceary は、作業ログと audit log をひとつのローカルストアに集約し、
複数の AI ツールから同じ履歴を読み書きできるようにすることを目指します。

## 機能

- 作業ログと audit log を SQLite にローカル保存する
- テキスト検索と日付範囲検索を提供する
- Claude Code / Codex / Gemini 間で MCP 経由の文脈共有を行う
- Claude Code / Codex / Gemini の hooks からセッション境界と shell audit を取り込めるようにする
- git remote URL でリポジトリを識別する
- `client` / `agent` / `session_id` による attribution を保持する
- 保持期間と `gc` により長期的なデータ肥大化を抑える

## インストール

### go install

```sh
go install github.com/duck8823/traceary@latest
```

### 事前ビルド済みバイナリ

タグ付き release では macOS / Linux 向け archive を GitHub Releases に公開します。
release 導線とローカル snapshot build は [`docs/release/README.ja.md`](./docs/release/README.ja.md) を参照してください。

## 対応プラットフォーム

- 事前ビルド済み release archive は macOS / Linux 向けに公開します
- core CLI と `traceary mcp-server` は macOS / Linux で継続的に検証しています
- `go install` によって他の Go 対応 Unix 系環境でも動く可能性はありますが、現時点の support promise には含めていません
- hooks は bash ベースの Unix 系環境を前提にしており、Windows の PowerShell / `cmd.exe` workflow はまだ正式対応していません
- Windows で使う場合は、現時点では WSL などの POSIX 互換環境を推奨します

## CLI 言語

- デフォルトの help / success message / よくある error は英語です
- 日本語 UI を使いたい場合は `TRACEARY_LANG=ja` を指定してください
- `--json` 出力は言語設定の影響を受けません

## クイックスタート

`traceary init` は任意です。すべてのコマンドは必要に応じて SQLite DB を自動作成し、マイグレーションを適用します。`init` は DB パスを明示的に先に作りたいときや、書き込み権限を事前に確認したいときだけ使います。

### 最初の 1 回を成功させる

まずは、DB 作成、session 記録、event lookup が通る最短ループです。

```sh
sid=$(traceary session start --client dogfood --agent codex)
event_id=$(traceary log --client dogfood --agent codex --session-id "$sid" --id-only "失敗したテストを調査している")
traceary show "$event_id" --json
```

### 同じ session に command output を足す

最初の note が通ったら、command audit を追加して検索で引き直します。

```sh
event_id=$(
  traceary audit \
    --client dogfood \
    --agent codex \
    --session-id "$sid" \
    --id-only \
    --command "go test ./..." \
    --input '{"stdin":""}' \
    --output '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}'
)
traceary search boom --json
traceary show "$event_id" --json
traceary session active
```

出力例:

```text
$ traceary init
Initialized: /Users/you/.config/traceary/traceary.db

$ traceary session start --client dogfood --agent codex
session-1ceee1eaa50a31687cfdb2c8a6fcc85d

$ traceary audit --id-only ...
0dc6d0c579df5e539c27df56e131570a

$ traceary search boom --json
[
  {
    "event_id": "0dc6d0c579df5e539c27df56e131570a",
    "kind": "command_executed",
    "message": "go test ./..."
  }
]

$ traceary show 0dc6d0c579df5e539c27df56e131570a --json
{
  "event": {
    "kind": "command_executed",
    "message": "go test ./..."
  },
  "command_audit": {
    "output": "{\"stdout\":\"panic: boom\",\"stderr\":\"stacktrace\",\"exitCode\":1}"
  }
}

$ traceary session active
session-1ceee1eaa50a31687cfdb2c8a6fcc85d
```

補足:

- `traceary session active` は既定で `24h` を超えた session を stale とみなします
- 古い未終了 session を確認したいときは `traceary session active --allow-stale` を使います
- `traceary session start` は既定で script-friendly な session ID をそのまま出力します
- 最初の導線ではなく全コマンドを見たいときは `docs/cli/README.ja.md` を見てください

この時点で分かる価値:

- 過去のコマンド出力を文字列検索で引ける
- いま使っている session ID を手で覚えなくてよい
- 1件のイベント詳細を取り出して別の AI に渡しやすい
- 大きすぎる audit payload は DB を膨らませすぎないよう切り詰められる

## コアコマンド

現時点の主要コマンド:

```sh
traceary init
traceary log <message>
traceary audit [<command> <input> <output>]
traceary search <query>
traceary list
traceary context
traceary handoff
traceary session start
traceary session end
traceary session latest
traceary session active
traceary show <event-id>
traceary doctor
traceary backup create --output <path>
traceary backup restore --input <path>
traceary hooks print --client <claude|codex|gemini>
traceary hooks install --client <claude|codex|gemini>
traceary hooks guide --client <claude|codex|gemini>
traceary mcp-server
traceary gc
```

shell script からは、mutating command に `--id-only` を付けると、人間向けテキストを解析せずに識別子だけを受け取れます。

```sh
traceary log --id-only "Investigating failing tests"
traceary audit --id-only --command "go test ./..." --input '{}' --output '{}'
traceary session end --session-id "$sid" --id-only
```

`search --kind` でよく使う例:

```sh
traceary search --kind command_executed
traceary search --kind note
traceary search --kind session_started
```

- 有効値: `note`, `command_executed`, `reviewed`, `session_started`, `session_ended`
- alias: `audit` = `command_executed`

`list` と `search` は `ORDER BY created_at DESC, event_id DESC` による安定した offset pagination を使います。
次ページが欲しい場合は、同じ filter 条件のまま `--offset` を増やしてください。

```sh
traceary list --limit 20 --offset 20
traceary search boom --limit 20 --offset 40 --json
```

`traceary session active` は既定で `--stale-after 24h` を使います。古い未終了 session も見たい場合は `--allow-stale` を付けてください。

`traceary session start` は生成された session ID をそのまま出力します。`traceary session end` は、終了対象の session ID は呼び出し側が既に知っている前提で、記録した event ID を出力します。

Hooks 導入: [`docs/hooks/README.ja.md`](./docs/hooks/README.ja.md)

Full CLI reference: [`docs/cli/README.ja.md`](./docs/cli/README.ja.md)

Environment variables and runtime assumptions: [`docs/environment/README.ja.md`](./docs/environment/README.ja.md)

MCP integration: [`docs/mcp/README.ja.md`](./docs/mcp/README.ja.md)

バックアップとマシン移行: [`docs/backup/README.ja.md`](./docs/backup/README.ja.md)

すべてのコマンドは SQLite path を `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db` の順で解決します。

`traceary audit` は既定で input/output をそれぞれ `64 KiB` まで保存します。より厳しくしたい場合は `--max-input-bytes`, `--max-output-bytes`, `TRACEARY_MAX_AUDIT_INPUT_BYTES`, `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` を使ってください。切り詰めが発生したときは CLI に通知を出します。

`traceary audit` は SQLite に書き込む前に、一般的な secret っぽい値（例: `Authorization:` header、`TOKEN=...`、JSON の `access_token`、private key block）も既定で伏せ字化します。これは完全な DLP ではなく best-effort の保護です。raw payload を意図的に残したい場合だけ `--allow-secrets` または `TRACEARY_ALLOW_SECRETS=true` を使ってください。

CLI の失敗は stderr に plain text の `Error: ...` 形式で出力されます。hooks や shell script から JSON ログを剥がさず扱えるようにするためです。

## ライセンス

MIT License です。詳細は [`LICENSE`](./LICENSE) を参照してください。

## スコープ外

- セマンティック検索 / 埋め込みベクトル
- チーム共有やクラウド同期
- Web UI / ダッシュボード
- エンタープライズ向け監査 / RBAC
- ファイル状態の完全再現
- リアルタイム可視化
