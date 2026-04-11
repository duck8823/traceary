# イベントライフサイクル

[English](./lifecycle.md)

Traceary が各 AI エージェントクライアントでイベントを記録する仕組みを説明します。

## クライアント別ライフサイクル

### Claude Code (Tier 1: フル対応)

```
SessionStart → [UserPromptSubmit → PostToolUse]* → (PreCompact → PostCompact →)* → SessionEnd
```

| Hook イベント | Matcher | Traceary イベント種別 | 説明 |
|---|---|---|---|
| SessionStart | `*` | `session_started` | セッション開始（workspace 解決を含む） |
| SessionStart | `compact` | — | compact-summary を新しいコンテキストに注入（stdout 経由） |
| UserPromptSubmit | `*` | `prompt` | ユーザーの指示テキスト |
| PostToolUse | `Bash` | `command_executed` | シェルコマンド（入出力・終了コード付き） |
| PostToolUse | `mcp__.*` | `command_executed` | MCP ツール呼び出し |
| PostToolUseFailure | `Bash`, `mcp__.*` | `command_executed` | 失敗したツール実行（`failures_only` でフィルタ可能） |
| PostCompact | `*` | `compact_summary` | コンテキスト圧縮時の構造化サマリー |
| SessionEnd | `*` | `session_ended` | セッション終了 |

### Codex CLI (Tier 2: 部分対応)

```
SessionStart → [PostToolUse]* → Stop
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| SessionStart | `session_started` | セッション開始 |
| PostToolUse | `command_executed` | ツール実行 |
| Stop | `session_ended` | セッション終了（SessionEnd ではなく Stop を使用） |

**制限**: `compact` hooks なし、`prompt` 記録なし、failure 専用イベントなし。

### Gemini CLI (Tier 3: 基本対応)

```
SessionStart → [AfterTool]* → SessionEnd
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| SessionStart | `session_started` | セッション開始 |
| AfterTool | `command_executed` | ツール実行 |
| SessionEnd | `session_ended` | セッション終了 |

**制限**: `compact` hooks なし、`prompt` 記録なし、failure 専用イベントなし。

## イベント種別

| 種別 | 説明 | ソース |
|------|------|--------|
| `note` | 自由テキストログ | CLI `traceary log` / MCP `add_log` |
| `command_executed` | コマンド・ツール実行記録 | PostToolUse hooks |
| `reviewed` | レビュー結果 | CLI / MCP |
| `session_started` | セッション開始境界 | SessionStart hooks |
| `session_ended` | セッション終了境界 | SessionEnd / Stop hooks |
| `compact_summary` | コンテキスト圧縮時の構造化サマリー | PostCompact hook |
| `prompt` | ユーザーの指示テキスト | UserPromptSubmit hook |

## データフロー

```
AI クライアント (Claude Code / Codex CLI / Gemini CLI)
  │
  ├─ Hook / Extension イベント
  │    │
  │    ▼
  │  traceary-*.sh (bash スクリプト: ~/.config/traceary/hook-scripts/)
  │    │
  │    ▼
  │  traceary CLI (log / audit / session start|end)
  │    │
  │    ▼
  │  SQLite (~/.config/traceary/traceary.db)
  │
  └─ MCP server (stdio トランスポート)
       │
       ▼
     traceary mcp-server → SQLite
```

## Hook スクリプトマッピング

| スクリプト | 用途 | 対応クライアント |
|------------|------|------------------|
| `traceary-session.sh` | セッション開始・終了 | 全クライアント |
| `traceary-audit.sh` | コマンド・ツール監査 | 全クライアント |
| `traceary-compact.sh` | compact サマリー記録 | Claude Code |
| `traceary-prompt.sh` | ユーザー prompt 記録 | Claude Code |
| `common.sh` | 共通ヘルパー関数 | 全クライアント |
