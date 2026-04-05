# Traceary

[English](./README.md)

Traceary は、AI エージェントの作業ログと audit log をローカルに記録・検索する CLI / MCP サーバーです。

Claude Code / Codex / Gemini などの AI コーディングエージェントが残すセッションメモやツール実行履歴を SQLite に保存し、
CLI と MCP の両方から同じ文脈を参照できるようにします。

## Why

AI エージェントを日常的に使うと、次のような問題が起きます。

- `clear` / `compact` でセッション文脈が失われやすい
- Git 履歴では「何をしたか」は追えても、「なぜそうしたか」は残りにくい
- どのエージェントがどのコマンドを実行したかを確認しづらい
- Claude / Codex / Gemini 間で文脈が分断されやすい
- 並列セッションや worktree 移動で履歴が追いにくくなる
- ログが増え続けるため、保持期間や GC が必要になる

Traceary は、作業ログと audit log をひとつのローカルストアに集約し、
複数の AI ツールから同じ履歴を読み書きできるようにすることを目指します。

## Features

- 作業ログと audit log を SQLite に記録する
- テキスト検索と日付範囲検索を提供する
- Claude Code / Codex / Gemini 間で MCP 経由の文脈共有を行う
- git remote URL ベースでリポジトリを識別し、worktree 移動に耐える
- `session_id` と `client` / `agent` による attribution を保持する
- 保持期間と `gc` により DB サイズを制御する

## How it works

### CLI

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

### MCP server

Traceary は stdio transport で次のツールを公開します。

- `add_log`
- `add_audit`
- `search`
- `get_context`

これにより、Claude Code / Codex / Gemini が同じローカルログを読み書きできます。

### Storage

- SQLite (`~/.config/traceary/traceary.db`)
- local-first、外部 API 不要
- git remote URL によるリポジトリ紐付け
- 長期運用を前提にした DB 上限管理とログローテーション

### Data model

各レコードは、マルチエージェント運用に必要なメタデータを持ちます。

- `client` (`claude-code` / `codex` / `gemini`)
- `agent` (`reviewer`, `planner` など)
- `session_id`
- git remote URL 由来のリポジトリ識別子

## Hooks / Integration

v0.1 では、各 AI エージェントランタイムから次のように接続する想定です。

- Session start / end hook でセッション境界を記録する
- Post-tool hook で Bash コマンドの audit log を記録する
- セッション開始時に直近の失敗履歴を注入し、同じ失敗の再試行を減らす

## Scope

### v0.1

- Go によるシングルバイナリ実装
- SQLite ベースのローカルストレージ
- logging / audit / search / list / session boundary / GC を持つ CLI
- AI ツール間で共有するための MCP サーバー
- DB を肥大化させないための保持ポリシー

### Out of scope

- semantic search / embeddings
- チーム共有やクラウド同期
- Web UI / dashboard
- enterprise audit / RBAC
- ファイル状態の完全再現
- ライブ observability
