# ホスト hook 対応マトリクス

[English](./host-coverage.md)

このページでは各ホスト AI agent について、Traceary の [ライフサイクルイベント](./lifecycle-events.ja.md) が実際の hook に紐づいているか、ホストが公開しているが Traceary 側で未配線か、そもそもホストが公開していないかを記録する。

凡例:

- `●` 配線済（Traceary パッケージ統合に含まれる）
- `○` ホスト側に hook はあるが Traceary 未配線
- `✕` ホスト自体が公開していない

**最終確認日: 2026-07-14（Grok Build 0.2.99 の live payload、Antigravity CLI 1.1.1 と現行公式 hook contract を確認。Gemini CLI は 2026-06-10 に 0.43.0 で再確認済み）。** Traceary 統合パッケージのバンプ、もしくは host CLI のリリースで hook surface が変化したときに更新する。

> **v0.21.1 注記:** このマトリクスに掲載されている Gemini CLI の hook カバレッジは**レガシー互換のみ**です。Gemini CLI はレガシーの Google AI エージェントホストであり、後継は Antigravity（`/Applications/Antigravity.app`）です。**v0.21.1 以降、Antigravity はサポート対象の hook client** であり、文書化された公開 hook surface に対する packaged plugin（`integrations/antigravity-plugin/`）を提供します。[Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) を参照してください。

## ライフサイクルイベント → ホスト hook マトリクス

| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.144.1 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Antigravity (`antigravity-plugin`) | Grok Build 0.2.99 | 確認方法 |
|---|---|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | ● `PreInvocation`（`conversationId` を key にした冪等開始。Antigravity に `SessionStart` はない） | ● `SessionStart`（native の `agent=grok`） | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | ● `Stop`（直接の prompt field は無い。`transcriptPath` の最新 `USER_INPUT` / `USER_EXPLICIT` 行から復元） | ● `UserPromptSubmit`（`prompt`、native の `agent=grok`） | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | ● `PreToolUse` + `PostToolUse`（`run_command`。command args は `conversationId + stepIdx` で 2 event をまたいで突き合わせ） | ● `PreToolUse` の検証 + `PostToolUse` の完了監査（input/result は同一 payload。missing/denied read は失敗 result variant） | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | ● `Stop`（`transcriptPath`、best-effort な寛容 JSONL 走査） | ● `Stop`（`updates.jsonl` から現在の prompt の message chunk を best-effort 取得。model field はない） | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` で resume) | ● `PreCompact` + `PostCompact` marker（`trigger` のみ。Codex は圧縮後サマリー本文を公開しない） | ● `PreCompress` (marker のみ — Gemini に post-compress 側 hook はない) | ✕ 文書化された compact hook なし | ● `PreCompact` + `PostCompact`（native で live 確認済みの `source` marker。summary 本文なし） | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ host のセッション終了信号なし — Codex `Stop` は応答ごとの turn 境界でありセッション終了ではない (#1170)。終了は MCP `manage_session` または stale GC 経由 | ● `SessionEnd` | ✕ host のセッション終了信号なし — Antigravity `Stop` は execution 単位の境界でありセッション終了ではない (#1170)。終了は MCP `manage_session` または stale GC 経由 | ○ `SessionEnd` は文書化済みだが、headless 完了・TUI `/quit`・TUI `/new` の probe では未発火 | `traceary list events --kind session_ended --limit 5` |

> **Antigravity headless `agy --print`:** 現行 CLI は `PreInvocation`、必要に応じて `PreToolUse`/`PostToolUse`、および `transcriptPath` 付き `Stop` を発行します。Traceary は Stop で prompt と transcript を復元します。実行時の欠落は `antigravity-event-coverage` が DB 証拠から検出します。hook event は `client=hook`, `agent=antigravity` で記録されるため、`traceary list --agent antigravity` で確認してください。

> **Grok Build 0.2.99 contract:** sanitized な空 workspace で `SessionStart`、`UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`Stop`、`PreCompact`、`PostCompact` を live capture しました。`PostToolUseFailure`、`PermissionDenied`、`SessionEnd` は文書化されていますが、対応する failure / denial / end probe では発火せず、`FileNotFound` と `PermissionDenied` は `PostToolUse.toolResult` の variant として返りました。`StopFailure` は意図的に API error を起こしておらず、Grok subagent は外部 agent policy gate に従って無効のままです。そのため Traceary は未観測 event を v0.23 の対応対象として主張しません。field 単位の証拠は [`host-contract.json`](./host-contract.json) にあります。

> **Grok native runtime:** `traceary hooks install --client grok` は `.grok/hooks/traceary.json`（`--global` では `~/.grok/hooks/traceary.json`）へ書き込みます。core と compact event は `client=hook`、`agent=grok` で保存します。`Stop` は turn 境界のままです。subagent capture は parent/child identifier payload を検証するまで利用不可です。

### Traceary 未配線のホスト hook

上のライフサイクルマトリクスに既出の hook はここでは省略している。

| Host | Hook | 状態 | 備考 |
|---|---|---|---|
| Claude Code | `SubagentStart` (`PreToolUse matcher=Task\|Agent`) | ● 配線済（subagent 補足、lifecycle event ではない） | `note` body marker として記録 |
| Claude Code | `SubagentStop` | ● 配線済（subagent 補足） | 同上 |
| Claude Code | `Notification`, `PreToolUse` (他 matcher), `StopFailure`, `UserPromptExpansion`, `PermissionRequest`, `PermissionDenied`, `PostToolBatch`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `Elicitation`, `ElicitationResult` | ○ 利用可 | 現行パッケージでは未配線 |
| Codex CLI | `SubagentStart`, `SubagentStop` | ● 配線済（child session capture） | `agent_id` で対応付け、`agent_type` を child agent 名に使用 |
| Codex CLI | `PreToolUse`, `PermissionRequest` | ○ 利用可 | 未配線 |
| Gemini CLI | `BeforeTool`, `BeforeToolSelection`, `BeforeModel`, `AfterModel`, `Notification` | ○ 利用可 | 未配線 |
| Antigravity | `run_command` 以外の tool に対する `PreToolUse` | ○ 利用可 | audit 対象は `run_command` のみ |
| Grok Build | `PostToolUseFailure`, `PermissionDenied`, `SessionEnd`, `StopFailure` | ○ 文書化済み、0.2.99 live 未確認 | live payload を確認するまで Traceary では利用不可。tool denial は現状 `PostToolUse` で到達 |
| Grok Build | `SubagentStart`, `SubagentStop` | ○ 文書化済み、policy gate により probe 保留 | parent/child field contract は未確定 |
| Grok Build | `Notification` | ○ 文書化済み、未検証 | Traceary lifecycle event への対応付けがなく、利用不可 |

## ホスト別参照

- Claude Code: https://code.claude.com/docs/en/hooks · パッケージ config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: Codex CLI 0.144.1 の公式 hook reference（`SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`）。 パッケージ config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: ローカルインストール同梱の hooks reference (`/opt/homebrew/Cellar/gemini-cli/0.43.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`。文書化された hook surface に post-compress event はなく、`PreCompress` は compression 前に非同期で発火する advisory-only hook). パッケージ config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)
- Antigravity: 公開 hook surface は https://www.antigravity.google/docs/hooks および https://antigravity.google/assets/docs/editor/ide-hooks.md、plugin packaging は https://antigravity.google/assets/docs/cli/cli-plugins.md に文書化（2026-06-20 JST 確認）。パッケージ config: [`integrations/antigravity-plugin/hooks.json`](../../integrations/antigravity-plugin/hooks.json)
- Grok Build: 公式 hook surface は https://docs.x.ai/build/features/hooks（最終更新 2026-07-02）。0.2.99 の live payload contract は [`host-contract.json`](./host-contract.json)、sanitized fixture は [`presentation/cli/testdata/grok_hooks/v0.2.99`](../../presentation/cli/testdata/grok_hooks/v0.2.99/) を参照

## 更新方法

このマトリクスは手書きのスナップショット。更新手順:

1. 確認したい host CLI をバンプ／再インストール。
2. ホスト側 hook reference と上の表を diff。
3. `●` セルごとに「確認方法」コマンドを実行し、`~/.config/traceary/traceary.db` に新しい行があることを確認。
4. ページ冒頭の **最終確認日** を更新。

`/schedule` 経由の日次 drift check は #814 で配線する。
