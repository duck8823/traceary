# ホスト hook 対応マトリクス

[English](./host-coverage.md)

このページでは各ホスト AI agent について、Traceary の [ライフサイクルイベント](./lifecycle-events.ja.md) が実際の hook に紐づいているか、ホストが公開しているが Traceary 側で未配線か、そもそもホストが公開していないかを記録する。

凡例:

- `●` 配線済（Traceary パッケージ統合に含まれる）
- `○` ホスト側に hook はあるが Traceary 未配線
- `✕` ホスト自体が公開していない

**最終確認日: 2026-04-27 (Gemini CLI は 2026-06-10 に 0.43.0 で再確認済).** Traceary 統合パッケージのバンプ、もしくは host CLI のリリースで hook surface が変化したときに更新する。

> **v0.21.0 注記:** このマトリクスに掲載されている Gemini CLI の hook カバレッジは**レガシー互換のみ**です。Gemini CLI はレガシーの Google AI エージェントホストであり、後継は Antigravity（`/Applications/Antigravity.app`）です。**Antigravity は v0.21.0 において確認・サポートされた hook カバレッジはありません。** Antigravity の capability detection は #1195 で実装済みです。この調査（#1196）の結果、サポートされた公開 CLI/hook contract が確認されていないため、v0.21.0 では Antigravity の hook / package / release asset を意図的に提供しません。Google がサポートされた公開 CLI/hook contract を公開するまで、Antigravity の列はこのマトリクスに追加しません。

## ライフサイクルイベント → ホスト hook マトリクス

| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.125 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | 確認方法 |
|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` で resume) | ✕ Codex 0.125 に compact hook なし (upstream openai/codex#16098) | ● `PreCompress` (marker のみ — Gemini に post-compress 側 hook はない) | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ host のセッション終了信号なし — Codex `Stop` は応答ごとの turn 境界でありセッション終了ではない (#1170)。終了は MCP `manage_session` または stale GC 経由 | ● `SessionEnd` | `traceary list events --kind session_ended --limit 5` |

### Traceary 未配線のホスト hook

上のライフサイクルマトリクスに既出の hook はここでは省略している。

| Host | Hook | 状態 | 備考 |
|---|---|---|---|
| Claude Code | `SubagentStart` (`PreToolUse matcher=Task\|Agent`) | ● 配線済（subagent 補足、lifecycle event ではない） | `note` body marker として記録 |
| Claude Code | `SubagentStop` | ● 配線済（subagent 補足） | 同上 |
| Claude Code | `Notification`, `PreToolUse` (他 matcher), `StopFailure`, `UserPromptExpansion`, `PermissionRequest`, `PermissionDenied`, `PostToolBatch`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `Elicitation`, `ElicitationResult` | ○ 利用可 | 現行パッケージでは未配線 |
| Codex CLI | `PreToolUse`, `PermissionRequest`, `Notification` | ○ 利用可 | 未配線 |
| Gemini CLI | `BeforeTool`, `BeforeToolSelection`, `BeforeModel`, `AfterModel`, `Notification` | ○ 利用可 | 未配線 |

## ホスト別参照

- Claude Code: https://code.claude.com/docs/en/hooks · パッケージ config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: upstream binary `codex-cli 0.125.0` — hook surface はローカルインストールのバイナリ文字列 (`SessionStart`, `Stop`, `PreToolUse`, `PostToolUse`, `Notification`, `PermissionRequest`, `UserPromptSubmit`) から推定。compact hook tracking: openai/codex#16098. パッケージ config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: ローカルインストール同梱の hooks reference (`/opt/homebrew/Cellar/gemini-cli/0.43.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`。文書化された hook surface に post-compress event はなく、`PreCompress` は compression 前に非同期で発火する advisory-only hook). パッケージ config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)

## 更新方法

このマトリクスは手書きのスナップショット。更新手順:

1. 確認したい host CLI をバンプ／再インストール。
2. ホスト側 hook reference と上の表を diff。
3. `●` セルごとに「確認方法」コマンドを実行し、`~/.config/traceary/traceary.db` に新しい行があることを確認。
4. ページ冒頭の **最終確認日** を更新。

`/schedule` 経由の日次 drift check は #814 で配線する。
