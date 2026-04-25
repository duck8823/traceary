# Hook 契約

[English](./contract.md)

このページでは、AI エージェントごとに hook でどこまで自動記録できるかを整理しています。

## 対応レベル

### Tier 1: フル対応 (Claude Code)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| PostToolUse | `Bash` | シェルコマンド監査を exit code 付きで記録 |
| PostToolUse | `mcp__.*` | MCP ツール監査を記録 |
| PostToolUse | `Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode` | Claude Code 組み込みツール（ファイル I/O・検索・agent・web・plan モード終了）の呼び出しを記録（v0.8-6 で追加、v0.8-6b で `NotebookRead` と `ExitPlanMode` を追加） |
| PostToolUseFailure | `Bash`, `mcp__.*`, 組み込みツール | 失敗したツール実行を記録 |
| PostCompact | `*` | compact サマリーを記録 |
| UserPromptSubmit | `*` | ユーザーの指示テキストを記録 |
| Stop | `*` | `transcript_path` から最後の assistant メッセージを読み取り `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.extra_patterns` を適用） |
| SessionStart (compact) (予定) | `compact` | stdout 経由でコンテキストポインタを注入 |

### Tier 2: 部分対応 (Codex)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | (全て) | セッション開始を記録 |
| UserPromptSubmit | (全て) | ユーザーの指示テキストを記録 |
| Stop | (全て) | セッション終了を記録し、`last_assistant_message` から最終 assistant メッセージを `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.extra_patterns` を適用） |
| PostToolUse | (全て) | ツール監査を記録 |

**制限**: SessionEnd なし（Stop を使用）、compact hooks なし、failure 専用イベントなし。

### Tier 3: 基本対応 (Gemini CLI)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| AfterAgent | `*` | `prompt_response` から agent 応答を `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.extra_patterns` を適用） |
| AfterTool | `*` | ツール監査を記録 |

**制限**: compact hooks なし、failure 専用イベントなし。Gemini には Stop event が存在しないため、transcript 取得は `AfterAgent` に紐付けている。

## 共通動作

全レベル共通:
- セッション開始時に解決した workspace を state ファイルに保存し、audit はそこから workspace を読み取る
- エージェントタイプ解決: `agent_type` フィールド → 階層的エージェント名（Claude のみ）
- `tool_response.exitCode` から exit code を抽出（利用可能時）
- MCP ツール名 fallback: `tool_input.command` → `tool_name`

Claude Task subagent capture:
- `PreToolUse:Task` は現在 active な直近の親セッション配下に child session を開始する。その child がさらに Task を開始した場合、grandchild は top-level session ではなく child にリンクする。
- active Task state は parent session ごとに管理するため、sibling の `spawn_order` は各 `parent_session_id` 内で独立して採番される。
- `tool_use_id` が欠落している場合は `event_id` から安定した Task key を合成する。後続の `PostToolUse` / `SubagentStop` が `tool_use_id` を渡さない場合は、親配下の most-recent active child にフォールバックする。
- `SubagentStop` が到達しなかった orphan active Task entry は、hook state を次に読む時または新しい session start 時に 24 時間超で prune する。これにより host process の crash / kill 後に stale child state が後続 capture へ漏れることを防ぐ。

## 欠落機能のフォールバック

| 欠落機能 | フォールバック |
|---|---|
| Compact hooks | MCP `get_context` / `session_handoff` でオンデマンド取得 |
| Failure イベント | audit スクリプトで tool_response の exit code を解析 |
| エージェントタイプ | クライアント名のみ使用（例: `codex`, `gemini`） |

## 2026 Q2 ホスト別機能メモ

Traceary の managed hook 集合は、リリースをまたいで安定させるため新機能への追従を意図的に抑えています。2026 Q2 時点で利用可能な機能のうち、既定 install で **wire していないもの** を以下にまとめます。`traceary doctor` の `<client>-host-capabilities` チェックでも同じ内容を informational として出力します。

| ホスト | 新機能 | 状態 | Traceary の挙動 |
|---|---|---|---|
| Claude Code | `SubagentStop` (2026-01 から利用可能) | wire 済み | `traceary hook subagent-stop claude` 経由で `session_ended` + `[phase:subagent]` プレフィックスで記録 |
| Claude Code | `PreCompact` (2026-01 から利用可能) | wire 済み | `traceary hook compact claude pre-compact` 経由で `compact_summary` + `[phase:pre-compact]` プレフィックスで記録。`loadCompactSummary` が prefix を skip するため handoff / memory_pack は引き続き post-compact summary を返す |
| Codex CLI | `Stop.last_assistant_message` | wire 済み | Codex `Stop` event 上で既存の session-stop hook と並んで `traceary hook transcript codex` が起動し `transcript` event として記録 |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | install 単位で opt-in | `traceary memory import codex` は flag 状態に関わらず動作。flag は Codex 側の capture 挙動にしか影響しない |
| Gemini CLI | `AfterAgent.prompt_response` | wire 済み | Gemini `AfterAgent` event 上で `traceary hook transcript gemini` が起動し `transcript` event として記録 (Gemini には Stop event が存在しない) |
| Gemini CLI 0.38.x | Memory manager agent / auto-memory | プレビュー | Traceary Tier 3 surface はまだこれらの preview 信号を subscribe していない |

これらの preview 機能を有効化したいオペレーターはクライアント側の公式ドキュメントを参照してください。Traceary は Tier 1-3 表に記載した安定 capability のみを記録し、`doctor` の informational check はギャップを可視化するためのものであり、有効化を強制するものではありません。
