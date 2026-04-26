# ライフサイクルイベント

[English](./lifecycle-events.md)

このページでは Traceary の **canonical lifecycle event kind**（ふだん hook によって発行され、セッションの監査タイムライン (L1) を形成する 6 種類の `EventKind`）をまとめる。

完全な enum は [`domain/types/event_kind.go`](../../domain/types/event_kind.go) を参照。lifecycle 以外の 2 種類（`note`、`reviewed`）は operator 主導で発行されるためここでは扱わない。各クライアントの hook → event 対応は [イベントライフサイクル](../lifecycle.ja.md)、capability tier は [Hook Contract](./contract.ja.md) を参照。

## 一覧

| Kind | 発行タイミング | 主な hook | Body |
|---|---|---|---|
| `session_started` | エージェントセッション開始 | `SessionStart` (Claude / Codex / Gemini) | workspace + agent 識別子 |
| `prompt` | ユーザが指示を投入 | `UserPromptSubmit` (Claude / Codex) | 生プロンプト（redact 後） |
| `command_executed` | tool / shell 呼び出しが成功または失敗で終わる | `PostToolUse`, `PostToolUseFailure`, `AfterTool` | input / output / exit code（compact JSON、redact 後） |
| `transcript` | アシスタント応答ターンが推論・説明テキストで閉じる | `Stop` (Claude / Codex) | 最後のアシスタントメッセージ本文（redact 後） |
| `compact_summary` | ホスト側 context 圧縮で要約が生成される | `PostCompact`（現状 Claude のみ） | 構造化された compact summary |
| `session_ended` | エージェントセッション終了 | `SessionEnd` (Claude / Gemini) または `Stop` (Codex) | 任意の reason marker |

すべての event body は永続化前に built-in secret redaction と operator 設定の `redact.rules` / `redact.extra_patterns` を通る。

## 個別の詳細

### `session_started`

- `sessions` 行の開始境界として記録される。
- セッション ID 解決順: hook payload `session_id` → `TRACEARY_HOOK_STATE_DIR` のキャッシュ → `traceary session start` 生成 UUID。
- workspace 解決優先度: 正規化された `remote.origin.url` → ローカル git worktree root → 生 hook `cwd`。
- L2 / L3 (handoff、context pack) の絞り込みキーになる。

### `prompt`

- ユーザの指示を redact 後そのまま記録。`traceary timeline` / `traceary search` / L2 `get_context` の本体に出る。
- Claude Code (`UserPromptSubmit`), Codex CLI (`UserPromptSubmit`), Gemini CLI (`BeforeAgent`) すべてが発行（[host-coverage.ja.md](./host-coverage.ja.md) 参照）。
- Body marker なし（生テキスト）。アシスタント側は `transcript` と区別する。

### `command_executed`

- ホストが Traceary の audit hook 経由で実行する全 tool 呼び出しに対応。
- Body 形式（compact JSON）:
  - `command`: `tool_input.command`（Bash 等にある場合）
  - `input`: `tool_input` の compact JSON
  - `output`: `tool_response` の compact JSON、失敗時は `{error, is_interrupt}`
- 失敗 payload は Claude 側で `PostToolUseFailure` 経由となり、`traceary list events` の `failures_only` で絞り込める。
- 通常セッションで最も件数が多いイベント。検索 / timeline はほぼここを触る。

### `transcript`

- アシスタント応答ターンの末尾にある推論・説明テキストブロックを保存（Claude `Stop`、Codex `Stop` の `last_assistant_message`）。
- tool-use ブロックは含めない（それらは `command_executed` 側で既に捕捉済）。
- timeline の「エージェントが何を判断したか」側を補完し、tool I/O を再生せずに L2 summary を再構成可能にする。

### `compact_summary`

- ホスト側で context window が圧縮されたときに発行。
- 現状 Claude Code のみ（`PostCompact`）。Codex 0.125 に compact hook なし（upstream `openai/codex#16098`）。Gemini は `PreCompress` を持つが post-compress 側 event は無い — Traceary では #807 で `PreCompress` の marker 配線を予定。
- L2 で、`SessionStart` matcher `compact` 経由のセッション再開時に `sessions.summary` の seed として使う。

### `session_ended`

- `sessions` 行の終了境界として記録される。
- Claude / Gemini は専用の `SessionEnd` を持つ。Codex は `SessionEnd` を公開していないため `Stop` で代用（[host-coverage.ja.md](./host-coverage.ja.md) 参照）。
- best-effort: ホストが hook を発火させずに終了するケース（kill -9、シェルクラッシュ）もあり、dangling session は L2 reconciliation で吸収する。

## 関連ドキュメント

- [Event Lifecycle](../lifecycle.ja.md) — クライアント別 hook → event mapping。
- [Hook Contract](./contract.ja.md) — capability tier (Tier 1 / 2 / 3)。
- [ホスト hook 対応マトリクス](./host-coverage.ja.md) — host ごとの wired / available / unsupported。
- [Memory layers](../memory/README.ja.md) — これらのイベントが L1 / L2 / L3 に流れる構造。
