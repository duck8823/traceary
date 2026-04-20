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
| PostToolUse | `Read\|Edit\|Write\|MultiEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|NotebookEdit` | Claude Code 組み込みツール（ファイル I/O・検索・agent・web）の呼び出しを記録（v0.8-6 で追加） |
| PostToolUseFailure | `Bash`, `mcp__.*`, 組み込みツール | 失敗したツール実行を記録 |
| PostCompact | `*` | compact サマリーを記録 |
| UserPromptSubmit | `*` | ユーザーの指示テキストを記録 |
| Stop | `*` | `transcript_path` から最後の assistant メッセージを読み取り `transcript` event として記録（既知 secret の redaction を適用） |
| SessionStart (compact) (予定) | `compact` | stdout 経由でコンテキストポインタを注入 |

### Tier 2: 部分対応 (Codex)

| Hook イベント | Matcher | 動作 |
|---|---|---|
| SessionStart | (全て) | セッション開始を記録 |
| UserPromptSubmit | (全て) | ユーザーの指示テキストを記録 |
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

全レベル共通:
- セッション開始時に解決した workspace を state ファイルに保存し、audit はそこから workspace を読み取る
- エージェントタイプ解決: `agent_type` フィールド → 階層的エージェント名（Claude のみ）
- `tool_response.exitCode` から exit code を抽出（利用可能時）
- MCP ツール名 fallback: `tool_input.command` → `tool_name`

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
| Claude Code | `SubagentStop` (2026-01 から利用可能) | 利用可能 | subagent lineage は `PostToolUse` の `agent_type` から復元。専用 hook は wire していない |
| Claude Code | `PreCompact` (2026-01 から利用可能) | 利用可能 | compact は `PostCompact` 経由で記録。pre-compact 時点の snapshot は wire していない |
| Codex CLI | Memory feature flag (`~/.codex/config.toml`) | install 単位で opt-in | `traceary memory import codex` は flag 状態に関わらず動作。flag は Codex 側の capture 挙動にしか影響しない |
| Gemini CLI 0.38.x | Memory manager agent / auto-memory | プレビュー | Traceary Tier 3 surface はまだこれらの preview 信号を subscribe していない |

これらの preview 機能を有効化したいオペレーターはクライアント側の公式ドキュメントを参照してください。Traceary は Tier 1-3 表に記載した安定 capability のみを記録し、`doctor` の informational check はギャップを可視化するためのものであり、有効化を強制するものではありません。
