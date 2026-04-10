# Hook 契約

[English](./contract.md)

AI エージェントクライアント間の hook 能力を定義するドキュメントです。

## 能力ティア

### Tier 1: フル対応 (Claude Code)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| PostToolUse | `Bash` | シェルコマンド監査を exit code 付きで記録 |
| PostToolUse | `mcp__.*` | MCP ツール監査を記録 |
| PostToolUseFailure | `Bash`, `mcp__.*` | 失敗したツール実行を記録 |
| PostCompact (予定) | `*` | compact イベントを記録 |
| SessionStart (compact) (予定) | `compact` | stdout 経由でコンテキストポインタを注入 |

### Tier 2: 部分対応 (Codex)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | (全て) | セッション開始を記録 |
| Stop | (全て) | セッション終了を記録 |
| PostToolUse | (全て) | ツール監査を記録 |

**制限**: SessionEnd なし（Stop を使用）、compact hooks なし、failure 専用イベントなし。

### Tier 3: 基本対応 (Gemini CLI)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | `*` | セッション開始を記録 |
| SessionEnd | `*` | セッション終了を記録 |
| AfterTool | `*` | ツール監査を記録 |

**制限**: compact hooks なし、failure 専用イベントなし。

## 共通動作

全ティア共通:
- セッション開始時に repo を state ファイルに永続化し、audit は state から読み取る
- エージェントタイプ解決: `agent_type` フィールド → 階層的エージェント名（Claude のみ）
- `tool_response.exitCode` から exit code を抽出（利用可能時）
- MCP ツール名 fallback: `tool_input.command` → `tool_name`

## 欠落機能のフォールバック

| 欠落機能 | フォールバック |
|---|---|
| Compact hooks | MCP `get_context` / `session_handoff` でオンデマンド取得 |
| Failure イベント | audit スクリプトで tool_response の exit code を解析 |
| エージェントタイプ | クライアント名のみ使用（例: `codex`, `gemini`） |
