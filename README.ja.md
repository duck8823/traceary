# Traceary

[English](./README.md)

[Changelog](./CHANGELOG.ja.md)

[文書ガイド](./docs/README.ja.md)

[コントリビューション](./CONTRIBUTING.ja.md)

[リリースガイド](./docs/release/README.ja.md)

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

## CLI 言語

- デフォルトの help / success message / よくある error は英語です
- 日本語 UI を使いたい場合は `TRACEARY_LANG=ja` を指定してください
- `--json` 出力は言語設定の影響を受けません

## クイックスタート

まずは「日常の作業がどう変わるか」だけを見る最短導線です。

`traceary init` は任意です。すべてのコマンドは必要に応じて SQLite DB を自動作成し、マイグレーションを適用します。`init` は DB パスを明示的に先に作りたいときや、書き込み権限を事前に確認したいときに使います。

```sh
traceary init
sid=$(traceary session start --client dogfood --agent codex)
traceary log --client dogfood --agent codex --session-id "$sid" "失敗したテストを調査している"
event_id=$(
  traceary audit \
    --client dogfood \
    --agent codex \
    --session-id "$sid" \
    "go test ./..." \
    '{"stdin":""}' \
    '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}' |
    awk '{print $2}'
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

$ traceary audit ... | awk '{print $2}'
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

この時点で分かる価値:

- 過去のコマンド出力を文字列検索で引ける
- いま使っている session ID を手で覚えなくてよい
- 1件のイベント詳細を取り出して別の AI に渡しやすい
- 大きすぎる audit payload は DB を膨らませすぎないよう切り詰められる

## コアコマンド

現時点の主要コマンド:

```sh
traceary log <message>
traceary audit <command> <input> <output>
traceary search <query>
traceary list
traceary session start
traceary session end
traceary session latest
traceary session active
traceary show <event-id>
traceary gc
```

`search --kind` でよく使う例:

```sh
traceary search --kind command_executed
traceary search --kind note
traceary search --kind session_started
```

- 有効値: `note`, `command_executed`, `reviewed`, `session_started`, `session_ended`
- alias: `audit` = `command_executed`

`traceary session active` は既定で `--stale-after 24h` を使います。古い未終了 session も見たい場合は `--allow-stale` を付けてください。

`traceary session start` は生成された session ID をそのまま出力します。`traceary session end` は、終了対象の session ID は呼び出し側が既に知っている前提で、記録した event ID を出力します。

Hooks 導入: [`docs/hooks/README.ja.md`](./docs/hooks/README.ja.md)

すべてのコマンドは SQLite path を `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db` の順で解決します。

`traceary audit` は既定で input/output をそれぞれ `64 KiB` まで保存します。より厳しくしたい場合は `--max-input-bytes`, `--max-output-bytes`, `TRACEARY_MAX_AUDIT_INPUT_BYTES`, `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` を使ってください。切り詰めが発生したときは CLI に通知を出します。

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
