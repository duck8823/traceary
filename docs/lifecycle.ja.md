# イベントライフサイクル

[English](./lifecycle.md)

このページでは、Traceary が各 AI エージェントクライアントからどのイベントを受け取り、どう保存するかを説明します。

## 通常 capture の log grouping identity

Session はログの grouping であり、実行中・終了の状態ではありません。通常の Start は identity を冪等に登録し、End は境界と指定された refinement coverage を記録します。grouping や子孫を終了しません。古い終了フィールド・イベントは履歴として保持します。manual/context/メモリ抽出 と Active/ActiveOnly 互換検索は開始時刻や終了履歴によらず記録済み grouping を選びます。doctor と hook GC は stale 終了を合成しません。native receipt のない個別 callback は個別記録となり、任意のローカル spool receipt は同一 durable delivery/replay の重複排除だけを担当します。runtime episode ではありません。receipt のない旧 spool は native 証拠がなければ at-least-once です。producer だけの rollback は安全ではありません。旧 GC は終了を合成し、旧 reader は保存済み grouping を除外するため、復旧は隔離コピーまたは明示承認された writer freeze で行います。

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
| PostToolUse | `Bash` | `command_executed` | シェルコマンド（入出力付き） |
| PostToolUse | `mcp__.*` | `command_executed` | MCP ツール呼び出し |
| PostToolUse | 組み込み tools | `command_executed` | ファイル I/O・検索・agent・web・plan モード終了 (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`)。v0.8-6 で追加、v0.8-6b で拡張。 |
| PostToolUseFailure | `Bash`, `mcp__.*`, 組み込み tools | `command_executed` | 失敗したツール実行（`traceary list --failures` でフィルタ可能） |
| PostCompact | `*` | `compact_summary` | コンテキスト圧縮時の構造化サマリー |
| Stop | `*` | `transcript` | stop-hook の `transcript_path` から読み取った最後の assistant 発話（reasoning 等） |
| SessionEnd | `*` | `session_ended` | セッション終了 |

### Codex CLI (Tier 2: 部分対応)

```
SessionStart → [UserPromptSubmit → PostToolUse → Stop]*
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| SessionStart | `session_started` | セッション開始 |
| UserPromptSubmit | `prompt` | ユーザーの指示テキスト |
| PostToolUse | `command_executed` | ツール実行 |
| Stop | `transcript` | 各 turn の最終 assistant メッセージ。セッション終了ではなく turn 境界 (#1170) |

**制限**: host レベルのセッション終了信号なし — Codex は assistant 応答ごとに `Stop` を fire するため、記録済み grouping はどの境界の後でも追記できます。`compact` hook はなく、failure 専用イベントもありません。

### Gemini CLI (Tier 3: 基本対応) — *レガシー互換*

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

### Antigravity (Tier 2: 部分対応)

```
[PreInvocation → PreToolUse → PostToolUse → Stop]*
```

| Hook イベント | Traceary イベント種別 | 説明 |
|---|---|---|
| PreInvocation | `session_started` | `conversationId` をキーにした冪等なセッション開始・更新（Antigravity に `SessionStart` はない） |
| PreToolUse (`run_command`) | — | 提案された `{CommandLine, Cwd}` を `conversationId + stepIdx` をキーに保存。block しない |
| PostToolUse (`run_command`) | `command_executed` | 同一 step の `PreToolUse` のコマンドと突き合わせて監査を記録（step の `error` 付き） |
| Stop | `transcript` | ホストが `Stop` を発行した場合の `transcriptPath` の turn transcript と turn 境界。セッションは閉じない (#1170) |

**制限**: `SessionStart` がなく（最初の信号は `PreInvocation`）、host のセッション終了信号もありません — Codex 同様 `Stop` は execution 単位の turn 境界なので、記録済み grouping はどの境界の後でも追記できます。audit 対象は `run_command` tool のみで、`transcriptPath` からの prompt/transcript 抽出は best-effort です。現在の interactive と headless `agy --print` は Stop を発行し、`antigravity-event-coverage` が recent DB 証拠から実行時の欠落を検出します。詳細は [capture matrix](./integrations/antigravity.ja.md) を参照してください。

> **v0.21 注**: Gemini CLI はレガシー互換パスです。後継ホストの Antigravity は v0.21.1 で Traceary のサポート対象 hook クライアントになりました（v0.21.0 は capability 診断のみ）。詳細は [Antigravity 統合状況](./integrations/antigravity.ja.md) を参照してください。

### 単発セッションのライフサイクル

単発セッションは、`traceary session run` が監視対象の子プロセスを起動した時点で
開始します。通常の完了を所有するのは wrapper だけであり、型付きの terminal
reason を記録してセッションを終了します。idle grouping を GC や doctor は終了しません。旧
[`session repair-one-shot`](operations/one-shot-repair.ja.md) は v0.43.0 (#2122) で廃止されました。

| ライフサイクル遷移 | 所有者 | 結果 |
| --- | --- | --- |
| 単発セッションを開始 | `traceary session run` | 監視対象の単発セッションを作成 |
| 単発セッションを終了 | 単発 wrapper | 型付きの terminal reason を記録 |


## イベント種別

| 種別 | 説明 | ソース |
|------|------|--------|
| `note` | 自由テキストログ | CLI `traceary log` |
| `command_executed` | コマンド・ツール実行の記録 | PostToolUse hooks |
| `reviewed` | レビュー結果 | CLI |
| `session_started` | セッション開始境界 | SessionStart hooks (Claude / Codex / Gemini)。PreInvocation (Antigravity) |
| `session_ended` | セッション終了境界 | SessionEnd hooks (Claude / Gemini)。Codex と Antigravity には host のセッション終了信号がない (#1170) |
| `compact_summary` | コンテキスト圧縮時の構造化サマリー | PostCompact hook |
| `prompt` | ユーザーの指示テキスト | UserPromptSubmit (Claude / Codex), BeforeAgent (Gemini) hooks |
| `transcript` | 最後の assistant メッセージの text ブロック（reasoning / 説明）。tool_use ブロックは `command_executed` に寄せるため除外する | Stop hook (Claude Code / Codex / Antigravity), AfterAgent (Gemini) |

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
```

## Hook スクリプトと役割

| スクリプト | 用途 | 対応クライアント |
|------------|------|------------------|
| `traceary hook session <client> <start|end|stop>` | セッション開始・終了の記録 | 全クライアント |
| `traceary hook audit <client>` | コマンド・ツール監査の記録 | 全クライアント |
| `traceary hook compact <client> <post-compact|session-start-compact>` | compact サマリーの記録 / compact resume 出力 | Claude Code |
| `traceary hook prompt <client>` | ユーザー prompt の記録 | Claude Code, Codex CLI, Gemini CLI |
| `traceary hook transcript <client>` | assistant 発話の transcript 記録（Claude / Codex / Antigravity は Stop hook、Gemini は AfterAgent 経由） | Claude Code, Codex CLI, Gemini CLI, Antigravity |
| `scripts/hooks/` 配下の shell wrapper | `traceary hook ...` へ転送する互換レイヤー | packaged integration / 既存導入環境 |
