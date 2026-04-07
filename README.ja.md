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
- git remote URL でリポジトリを識別する
- `client` / `agent` / `session_id` による attribution を保持する
- 保持期間と `gc` により長期的なデータ肥大化を抑える

## 予定しているコマンド

v0.1 で予定しているコマンド:

```sh
traceary log <message>
traceary audit <command> <input> <output>
traceary search <query>
traceary list
traceary session start
traceary session end
traceary gc
```

## スコープ外

- セマンティック検索 / 埋め込みベクトル
- チーム共有やクラウド同期
- Web UI / ダッシュボード
- エンタープライズ向け監査 / RBAC
- ファイル状態の完全再現
- リアルタイム可視化
