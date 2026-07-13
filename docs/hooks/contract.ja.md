# Hook 契約

[English](./contract.md)

このページでは、AI エージェントごとに hook でどこまで自動記録できるかを整理しています。

## 対応レベル

### Tier 1: フル対応 (Claude Code)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| PostToolUse | `Bash` | シェルコマンド監査を記録 |
| PostToolUse | `mcp__.*` | MCP ツール監査を記録 |
| PostToolUse | `Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode` | Claude Code 組み込みツール（ファイル I/O・検索・agent・web・plan モード終了）の呼び出しを記録（v0.8-6 で追加、v0.8-6b で `NotebookRead` と `ExitPlanMode` を追加） |
| PostToolUseFailure | `Bash`, `mcp__.*`, 組み込みツール | 失敗フラグ付きのツール監査を記録。payload はトップレベル `error` 文字列を持ち（`tool_response` も数値 exit code も無い）、exit code を読む代わりに監査を `failed` とマークする。`list --failures` はこのフラグを対象にする |
| PostCompact | `*` | compact サマリーを記録 |
| UserPromptSubmit | `*` | ユーザーの指示テキストを記録 |
| Stop | `*` | `transcript_path` から最後の assistant メッセージを読み取り `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.rules` / `redact.extra_patterns` を適用） |
| SessionStart (compact) (予定) | `compact` | stdout 経由でコンテキストポインタを注入 |

### Tier 2: 部分対応 (Codex)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | (全て) | セッション開始を記録 |
| SubagentStart | (全て) | `agent_id` で対応付けた child session を開始し、`agent_type` を child agent 名に使用 |
| SubagentStop | (全て) | `agent_id` で対応付けた child session を終了 |
| PreCompact | (全て) | `trigger` を `source_hook=pre_compact` の `compact_summary` 境界 marker として記録 |
| PostCompact | (全て) | `trigger` を `source_hook=post_compact` の `compact_summary` 境界 marker として記録 |
| UserPromptSubmit | (全て) | ユーザーの指示テキストを記録 |
| Stop | (全て) | `last_assistant_message` から最終 assistant メッセージを `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.rules` / `redact.extra_patterns` を適用）。セッション終了ではなく turn 境界として扱う |
| PostToolUse | (全て) | ツール監査を記録 |

**制限**: SessionEnd なし・host レベルのセッション終了信号なし — Codex は会話終了時ではなく assistant 応答ごとに `Stop` を fire するため、Traceary は `Stop` を turn 境界として扱い session を開いたままにする (#1170)。Codex session は明示的な終了信号 (MCP `manage_session`) または stale GC (`traceary session gc`、既定 24h) でのみ終了する。`PreCompact` / `PostCompact` は `manual` / `auto` の trigger のみを公開し、圧縮後サマリー本文は含まないため、どちらも境界 marker として記録する。failure 専用イベントも構造化された失敗信号もない。Codex は非ゼロ終了でも `PostToolUse` を fire するが、`tool_response` は exit code も error フィールドも持たない素の整形済み文字列のため、失敗した実行は通常の（フラグなし）監査として記録される。

### Tier 3: 基本対応 (Gemini CLI) — *レガシー互換*

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| BeforeAgent | `*` | `prompt` フィールドからユーザ prompt を `prompt` event として記録 |
| AfterAgent | `*` | `prompt_response` から agent 応答を `transcript` event として記録（既知 secret の redaction + オペレーター設定の `redact.rules` / `redact.extra_patterns` を適用） |
| AfterTool | `*` | ツール監査を記録 |
| PreCompress | `*` | `compact_summary` marker として記録（`trigger` のみ。Gemini には post-compress digest がない） |

**制限**: post-compress digest はなし — Gemini の `PreCompress` は advisory-only で compression 前に非同期で発火し、gemini-cli 0.43.0 同梱 hook reference の hook surface 全体（`BeforeTool` / `AfterTool` / `BeforeAgent` / `AfterAgent` / `BeforeModel` / `BeforeToolSelection` / `AfterModel` / `SessionStart` / `SessionEnd` / `Notification` / `PreCompress`）に post-compress event は存在しない。failure 専用イベントなし、PostCompact/SessionStart(compact) なし。Gemini には Stop event が存在しないため、transcript 取得は `AfterAgent` に紐付けている。失敗捕捉は部分的: `AfterTool` の nested `tool_response.error` は spawn/OS レベルエラー時のみ出る（その場合は `failed` とマーク）。通常の非ゼロ終了は `tool_response.llmContent` 内の `Exit Code: N` テキストとしてのみ現れ、Traceary は意図的に parse しないため、それらはフラグなしのまま。

> **v0.21 注**: 後継ホストの Antigravity は v0.21.1 から、文書化された公開 hook/plugin surface に対する hook client としてサポートしています（`PreInvocation` のセッション開始 / リフレッシュ、`PreToolUse` / `PostToolUse` の `run_command` 対応付け、ホストが発行した場合の `Stop` による transcript / turn boundary）。現在の headless `agy --print` は `transcriptPath` 付き `Stop` を発行し、Traceary はその file から prompt と response を復元します。上記 Gemini CLI の情報はレガシー互換として既存導入環境向けに残しています。詳細は [Antigravity hook / plugin](../integrations/antigravity.ja.md) を参照してください。

## 共通動作

全レベル共通:
- セッション開始時に解決した workspace を state ファイルに保存し、audit はそこから workspace を読み取る
- エージェントタイプ解決: `agent_type` フィールド → 階層的エージェント名（Claude および Codex の subagent hook）
- host が提供する場合は `tool_response.exitCode` から exit code を抽出。ただし現行のどの host も post-tool payload にこのフィールドを出さないため、実際には exit code ではなく構造的に失敗を検出する（上記の失敗フラグ行と下記 fallback 表を参照）
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
| Failure イベント | 失敗形状の payload から構造的な `failed` フラグを導出（Claude のトップレベル `error`、Gemini の `tool_response.error`）。`list --failures` は非ゼロ `exit_code` に加えて `failed = 1` も対象にする。構造化された失敗信号を出さない host（Codex、Gemini の通常の非ゼロ終了）はフラグなし監査として記録 |
| エージェントタイプ | クライアント名のみ使用（例: `codex`, `gemini`） |

## 2026 Q2 ホスト別機能メモ

Traceary の managed hook 集合は、リリースをまたいで安定させるため新機能への追従を意図的に抑えています。2026 Q2 時点で利用可能な機能のうち、既定 install で **wire していないもの** を以下にまとめます。`traceary doctor` の `<client>-host-capabilities` チェックでも同じ内容を informational として出力します。

| ホスト | 新機能 | 状態 | Traceary の挙動 |
|---|---|---|---|
| Claude Code | `SubagentStop` (2026-01 から利用可能) | wire 済み | `traceary hook subagent-stop claude` 経由で `session_ended` + `[phase:subagent]` プレフィックスで記録 |
| Claude Code | `PreCompact` (2026-01 から利用可能) | wire 済み | `traceary hook compact claude pre-compact` 経由で `compact_summary` + `[phase:pre-compact]` プレフィックスで記録。`loadCompactSummary` が prefix を skip するため handoff / memory_pack は引き続き post-compact summary を返す |
| Codex CLI | `Stop.last_assistant_message` | wire 済み | Codex `Stop` event 上で `traceary hook transcript codex` が起動し `transcript` event として記録。並走する `hook session codex stop` は session を閉じず turn 境界として動作する (#1170) |
| Codex CLI | `PreCompact` / `PostCompact` | wire 済み（marker のみ） | `trigger` を phase 別 `compact_summary` marker として保存。payload はサマリー本文を含まない |
| Codex CLI | `SubagentStart` / `SubagentStop` | wire 済み | `agent_id` で child session を対応付け、`agent_type` から agent 名を設定 |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | install 単位で opt-in | `traceary memory admin import codex` は flag 状態に関わらず動作。flag は Codex 側の capture 挙動にしか影響しない |
| Gemini CLI | `AfterAgent.prompt_response` | wire 済み | Gemini `AfterAgent` event 上で `traceary hook transcript gemini` が起動し `transcript` event として記録 (Gemini には Stop event が存在しない) |
| Gemini CLI | `BeforeAgent.prompt` | wire 済み | Gemini `BeforeAgent` event 上で `traceary hook prompt gemini` が起動し `prompt` event として記録 (Claude / Codex の `UserPromptSubmit` と同等) |
| Gemini CLI | `PreCompress.trigger` | wire 済み (marker のみ) | Gemini `PreCompress` event 上で `traceary hook compact gemini pre-compact` が起動し、`source_hook=pre_compact` で `trigger` 値を body にした `compact_summary` event として記録 (Gemini に post-compress digest はない) |
| Gemini CLI | Memory manager agent / auto-memory | experimental (0.43.0 で再確認済) | Traceary Tier 3 surface はまだこれらの experimental 信号を subscribe していない |

これらの preview 機能を有効化したいオペレーターはクライアント側の公式ドキュメントを参照してください。Traceary は Tier 1-3 表に記載した安定 capability のみを記録し、`doctor` の informational check はギャップを可視化するためのものであり、有効化を強制するものではありません。
