# ホスト hook 対応マトリクス

[English](./host-coverage.md)

このページでは各ホスト AI agent について、Traceary の [ライフサイクルイベント](./lifecycle-events.ja.md) が実際の hook に紐づいているか、ホストが公開しているが Traceary 側で未配線か、そもそもホストが公開していないかを記録する。

凡例:

- `●` 配線済（Traceary パッケージ統合に含まれる）
- `○` ホスト側に hook はあるが Traceary 未配線
- `✕` ホスト自体が公開していない

### ステータスの意味

ステータスはホストの能力ではなく、**Traceary 側の配線状態**を表します。

- **wired** — Traceary のパッケージ統合がこの lifecycle event を今日記録できます。慣例として wired セルは live 検証済みの host contract（payload probe 済み、fixture コミット済み）に裏付けられますが、matrix 自体が表明するのは配線の有無のみです。その host で Traceary DB に現れることが期待できるのは wired のイベントです。Verification 列の `traceary list events --kind <event>` で確認できます。
- **available** — ホストはこのイベントの hook や signal を公開していますが、Traceary は（まだ）配線していません。これは記録の保証では**ありません**。その host では該当イベントが Traceary DB に現れません。例: Grok の `SessionEnd`（文書化済みだが probe では未発火）、v0.29.0 以前の Kimi `PreCompact`/`PostCompact`。
- **unsupported** — ホストがこのイベントの利用可能な signal を公開していません（例: Codex/Antigravity の session end。代わりに MCP `manage_session` または stale GC で終了します）。

機械可読な [host contract](./host-contract.json) との関係: contract で `supported` / `best_effort`（live fixture あり）に分類されるイベントが **wired** セルの根拠になり、ホスト側に signal はあるが Traceary 未配線のイベント（contract では `unavailable` でもホスト側 signal が存在するもの。例: Grok `SessionEnd`）が **available** セルの、ホスト側に利用可能な signal がないイベントが **unsupported** セルの根拠になります。matrix 本体は `application/hostcoverage/matrix.json` にあり、この表はそこから生成されます — 下の生成ブロックは編集しないでください。

**最終確認日: 2026-07-19（Kimi Code 0.27.0 の live hook probe と統合 (#1393)。Grok Build 0.2.101 の未観測 hook 再 probe + 0.2.99 fixture、Antigravity CLI 1.1.1 と現行公式 hook contract。Gemini CLI は 2026-06-10 に 0.43.0 で再確認済み）。** Traceary 統合パッケージのバンプ、もしくは host CLI のリリースで hook surface が変化したときに更新する。

> **v0.21.1 注記:** このマトリクスに掲載されている Gemini CLI の hook カバレッジは**レガシー互換のみ**です。Gemini CLI はレガシーの Google AI エージェントホストであり、後継は Antigravity（`/Applications/Antigravity.app`）です。**v0.21.1 以降、Antigravity はサポート対象の hook client** であり、文書化された公開 hook surface に対する packaged plugin（`integrations/antigravity-plugin/`）を提供します。[Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) を参照してください。

## ライフサイクルイベント → ホスト hook マトリクス

<!-- host-coverage-matrix:begin -->
<!-- DO NOT EDIT: generated from application/hostcoverage/matrix.json via `go run ./cmd/repo-tooling docs generate-host-coverage`. -->
| Traceary lifecycle event | Claude Code (`claude-plugin`) | Codex CLI 0.144.1 (`plugins/traceary`) | Gemini CLI (`gemini-extension`) | Antigravity (`antigravity-plugin`) | Kimi Code 0.27.0 (`kimi-plugin`) | Grok Build 0.2.99 | 確認方法 |
|---|---|---|---|---|---|---|---|
| `session_started` | ● `SessionStart` | ● `SessionStart` | ● `SessionStart` | ● `PreInvocation`（`conversationId` を key にした冪等開始。Antigravity に `SessionStart` はない） | ● `SessionStart`（`source` = startup|resume。resume は同一 session_id で再発火し、冪等に記録） | ● `SessionStart`（native の `agent=grok`） | `traceary list events --kind session_started --limit 5` |
| `prompt` | ● `UserPromptSubmit` | ● `UserPromptSubmit` | ● `BeforeAgent` | ● `Stop`（直接の prompt field は無い。`transcriptPath` の最新 `USER_INPUT` / `USER_EXPLICIT` 行から復元） | ● `UserPromptSubmit`（`prompt` の content block 配列をテキスト化） | ● `UserPromptSubmit`（`prompt`、native の `agent=grok`） | `traceary list events --kind prompt --limit 5` |
| `command_executed` | ● `PostToolUse` + `PostToolUseFailure` (Bash, `mcp__.*`, built-in tool matcher) | ● `PostToolUse` | ● `AfterTool` | ● `PreToolUse` + `PostToolUse`（`run_command`。command args は `conversationId + stepIdx` で 2 event をまたいで突き合わせ） | ● `PostToolUse`（`tool_output` 文字列）+ `PostToolUseFailure`（`error` object を平坦化。`PreToolUse` は検証のみで記録しない） | ● `PreToolUse` の検証 + `PostToolUse` の完了監査（input/result は同一 payload。missing/denied read は失敗 result variant） | `traceary list events --kind command_executed --limit 5` |
| `transcript` | ● `Stop` | ● `Stop` (`last_assistant_message`) | ● `AfterAgent` | ● `Stop`（`transcriptPath`、best-effort な寛容 JSONL 走査） | ● `Stop`（best-effort: `session_index.jsonl` → session の `wire.jsonl` 最終ターンの `content.part` think/text block） | ● `Stop`（`updates.jsonl` から現在の prompt の message chunk を best-effort 取得。model field はない） | `traceary list events --kind transcript --limit 5` |
| `compact_summary` | ● `PostCompact` (+ `PreCompact` marker, `SessionStart matcher=compact` で resume) | ● `PreCompact` + `PostCompact` marker（`trigger` のみ。Codex は圧縮後サマリー本文を公開しない） | ● `PreCompress` (marker のみ — Gemini に post-compress 側 hook はない) | ✕ 文書化された compact hook なし | ● `PreCompact` + `PostCompact`（`trigger` の marker として記録。auto は live 観測済み、manual は未 probe。payload の token 数は保存しない） | ● `PreCompact` + `PostCompact`（native で live 確認済みの `source` marker。summary 本文なし） | `traceary list events --kind compact_summary --limit 5` |
| `session_ended` | ● `SessionEnd` | ✕ host のセッション終了信号なし — Codex `Stop` は応答ごとの turn 境界でありセッション終了ではない (#1170)。終了は MCP `manage_session` または stale GC 経由 | ● `SessionEnd` | ✕ host のセッション終了信号なし — Antigravity `Stop` は execution 単位の境界でありセッション終了ではない (#1170)。終了は MCP `manage_session` または stale GC 経由 | ● `SessionEnd`（`reason` = exit） | ○ `SessionEnd` は文書化済みだが、headless 完了・TUI `/quit`・TUI `/new` の probe では未発火 | `traceary list events --kind session_ended --limit 5` |
<!-- host-coverage-matrix:end -->

> **Antigravity headless `agy --print`:** 現行 CLI は `PreInvocation`、必要に応じて `PreToolUse`/`PostToolUse`、および `transcriptPath` 付き `Stop` を発行します。Traceary は Stop で prompt と transcript を復元します。実行時の欠落は `antigravity-event-coverage` が DB 証拠から検出します。hook event は `client=hook`, `agent=antigravity` で記録されるため、`traceary list --agent antigravity` で確認してください。

> **Grok Build contract（fixture 0.2.99、再 probe 0.2.101 / 2026-07-16）:** sanitized な空 workspace で `SessionStart`、`UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`Stop`、`PreCompact`、`PostCompact` を live capture しました。0.2.101 再 probe でも standalone な `PostToolUseFailure` / `PermissionDenied` / `SessionEnd` は発火せず、missing-file Read は `PostToolUse` 配下の `FileNotFound` でした。subagent 起動は `spawn_subagent` tool と tool audit のみで、`SubagentStart` / `SubagentStop` hook payload も parent/child identity も観測されませんでした。Traceary は未観測 hook を合成しません。field 単位の証拠は [`host-contract.json`](./host-contract.json) にあります。

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
| Grok Build | `PostToolUseFailure`, `PermissionDenied`, `SessionEnd`, `StopFailure` | ○ 文書化済み、0.2.99 および再 probe 0.2.101 でも live 未確認 | live payload を確認するまで Traceary では利用不可。missing-file と tool denial は現状 `PostToolUse` で到達 |
| Grok Build | `SubagentStart`, `SubagentStop` | ○ 文書化済み、0.2.101 で live 未発火 | 利用不可。spawn は `spawn_subagent` tool audit のみで parent/child hook payload なし（#1299） |
| Grok Build | `Notification` | ○ 文書化済み、未検証 | Traceary lifecycle event への対応付けがなく、利用不可 |

## ホスト別参照

- Claude Code: https://code.claude.com/docs/en/hooks · パッケージ config: [`integrations/claude-plugin/hooks/hooks.json`](../../integrations/claude-plugin/hooks/hooks.json)
- Codex CLI: Codex CLI 0.144.1 の公式 hook reference（`SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`）。 パッケージ config: [`plugins/traceary/hooks.json`](../../plugins/traceary/hooks.json)
- Gemini CLI: ローカルインストール同梱の hooks reference (`/opt/homebrew/Cellar/gemini-cli/0.43.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`。文書化された hook surface に post-compress event はなく、`PreCompress` は compression 前に非同期で発火する advisory-only hook). パッケージ config: [`integrations/gemini-extension/hooks/hooks.json`](../../integrations/gemini-extension/hooks/hooks.json)
- Antigravity: 公開 hook surface は https://www.antigravity.google/docs/hooks および https://antigravity.google/assets/docs/editor/ide-hooks.md、plugin packaging は https://antigravity.google/assets/docs/cli/cli-plugins.md に文書化（2026-06-20 JST 確認）。パッケージ config: [`integrations/antigravity-plugin/hooks.json`](../../integrations/antigravity-plugin/hooks.json)
- Grok Build: 公式 hook surface は https://docs.x.ai/build/features/hooks（最終更新 2026-07-02）。0.2.99 の live payload contract は [`host-contract.json`](./host-contract.json)、sanitized fixture は [`presentation/cli/testdata/grok_hooks/v0.2.99`](../../presentation/cli/testdata/grok_hooks/v0.2.99/) を参照

## 更新方法

上のライフサイクルマトリクス表は機械可読な正本
[`application/hostcoverage/matrix.json`](../../application/hostcoverage/matrix.json)
から生成されます。doctor の host-capability / event-coverage 期待値も同じ embedded matrix を読みます。

更新手順:

1. 確認したい host CLI をバンプ／再インストール。
2. `application/hostcoverage/matrix.json` を更新（status、日英 summary、`last_verified`）。
3. `go run ./cmd/repo-tooling docs generate-host-coverage` で marker 付き表セクションを再生成。
4. `●` セルごとに「確認方法」コマンドを実行し、`~/.config/traceary/traceary.db` に新しい行があることを確認。
5. `go run ./cmd/repo-tooling docs verify-host-coverage` を実行（CI でも強制）。

`/schedule` 経由の日次 drift check は #814 で配線する。
