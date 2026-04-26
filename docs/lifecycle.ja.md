# イベントライフサイクル

[English](./lifecycle.md)

このページでは、Traceary が各 AI エージェントクライアントからどのイベントを受け取り、どう保存するかを説明します。

## クライアント別ライフサイクル

### Claude Code (Tier 1: フル対応)

```
SessionStart → [UserPromptSubmit → PostToolUse]* → (PreCompact → PostCompact →)* → SessionEnd
```

| Hook イベント | Matcher | Traceary イベント種別 | 説明 |
|---|---|---|---|
| SessionStart | `*` | `session_started` | セッション開始。workspace 解決もここで行う |
| SessionStart | `compact` | — | compact-summary を新しいコンテキストへ stdout 経由で注入 |
| UserPromptSubmit | `*` | `prompt` | ユーザーが送った指示テキスト |
| PostToolUse | `Bash` | `command_executed` | シェルコマンド（入出力・終了コード付き） |
| PostToolUse | `mcp__.*` | `command_executed` | MCP ツール呼び出し |
| PostToolUse | 組み込み tools | `command_executed` | ファイル I/O・検索・agent・web・plan モード終了 (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`)。v0.8-6 で追加、v0.8-6b で拡張。 |
| PostToolUseFailure | `Bash`, `mcp__.*`, 組み込み tools | `command_executed` | 失敗したツール実行（`failures_only` でフィルタ可能） |
| PostCompact | `*` | `compact_summary` | コンテキスト圧縮時の構造化サマリー |
| Stop | `*` | `transcript` | stop-hook の `transcript_path` から読み取った最後の assistant 発話（reasoning 等） |
| SessionEnd | `*` | `session_ended` | セッション終了 |

### Codex CLI (Tier 2: 部分対応)

```
SessionStart → [UserPromptSubmit → PostToolUse]* → Stop
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| SessionStart | `session_started` | セッション開始 |
| UserPromptSubmit | `prompt` | ユーザーの指示テキスト |
| PostToolUse | `command_executed` | ツール実行 |
| Stop | `session_ended` | セッション終了（`SessionEnd` ではなく `Stop` を使う） |

**制限**: `compact` hook はなく、failure 専用イベントもありません。

### Gemini CLI (Tier 3: 基本対応)

```
SessionStart → [AfterTool]* → SessionEnd
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| SessionStart | `session_started` | セッション開始 |
| BeforeAgent | `prompt` | ユーザの指示テキスト（`prompt` フィールド） |
| AfterAgent | `transcript` | エージェントの最終応答（`prompt_response` フィールド） |
| AfterTool | `command_executed` | ツール実行 |
| PreCompress | `compact_summary` | pre-compact marker のみ（`trigger` フィールド。Gemini に post-compress event はない） |
| SessionEnd | `session_ended` | セッション終了 |

**制限**: post-compress digest はなし（Gemini の `PreCompress` は async marker のみ）、failure 専用イベントもありません。

## イベント種別

| 種別 | 説明 | ソース |
|------|------|--------|
| `note` | 自由テキストログ | CLI `traceary log` / MCP `record_event(type="log")` |
| `command_executed` | コマンド・ツール実行の記録 | PostToolUse hooks |
| `reviewed` | レビュー結果 | CLI / MCP |
| `session_started` | セッション開始境界 | SessionStart hooks |
| `session_ended` | セッション終了境界 | SessionEnd / Stop hooks |
| `compact_summary` | コンテキスト圧縮時の構造化サマリー | PostCompact hook |
| `prompt` | ユーザーの指示テキスト | UserPromptSubmit (Claude / Codex), BeforeAgent (Gemini) hooks |
| `transcript` | 最後の assistant メッセージの text ブロック（reasoning / 説明）。tool_use ブロックは `command_executed` に寄せるため除外する | Stop hook (Claude Code) |

## データフロー

```
AI クライアント (Claude Code / Codex CLI / Gemini CLI)
  │
  ├─ Hook / Extension イベント
  │    │
  │    ▼
  │  traceary hook ... （隠し Go runtime entrypoint）
  │    │
  │    ├─ packaged shell wrapper（必要な配布物だけの互換レイヤー）
  │    ▼
  │  SQLite (~/.config/traceary/traceary.db)
  │
  └─ MCP server (stdio トランスポート)
       │
       ▼
     traceary mcp-server → SQLite
```

## Hook スクリプトと役割

| スクリプト | 用途 | 対応クライアント |
|------------|------|------------------|
| `traceary hook session <client> <start|end|stop>` | セッション開始・終了の記録 | 全クライアント |
| `traceary hook audit <client>` | コマンド・ツール監査の記録 | 全クライアント |
| `traceary hook compact <client> <post-compact|session-start-compact>` | compact サマリーの記録 / compact resume 出力 | Claude Code |
| `traceary hook prompt <client>` | ユーザー prompt の記録 | Claude Code, Codex CLI, Gemini CLI |
| `traceary hook transcript <client>` | assistant 発話の transcript 記録（Stop hook 経由） | Claude Code |
| `scripts/hooks/` 配下の shell wrapper | `traceary hook ...` へ転送する互換レイヤー | packaged integration / 既存導入環境 |
