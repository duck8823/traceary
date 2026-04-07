# Traceary

[English](./README.md)

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

## クイックスタート

まずは「日常の作業がどう変わるか」だけを見る最短導線です。

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
初期化しました: /Users/you/.config/traceary/traceary.db

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

`traceary session active` は既定で `--stale-after 24h` を使います。古い未終了 session も見たい場合は `--allow-stale` を付けてください。

Hooks 導入: [`docs/hooks/README.md`](./docs/hooks/README.md)

## スコープ外

- セマンティック検索 / 埋め込みベクトル
- チーム共有やクラウド同期
- Web UI / ダッシュボード
- エンタープライズ向け監査 / RBAC
- ファイル状態の完全再現
- リアルタイム可視化
