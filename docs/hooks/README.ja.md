# Hooks ガイド

[English](./README.md)

Traceary は、Claude Code / Codex CLI / Gemini CLI から送られる hook イベントを、隠しサブコマンド `traceary hook ...` で受け取って session 境界、command audit、compact summary、prompt を記録します。

生成される hook 設定と配布済みの host package は、この Go runtime entrypoint を直接呼ぶ前提です。`scripts/hooks/` 配下の shell script は、配布物や既存環境との互換性を保つための薄いラッパーとして残していますが、主 runtime 実装ではありません。

生成済みの hook 設定は、対応しているクライアント設定ファイルであれば既存 JSON にマージして追加できます。既定では、既存設定を破壊的に置き換えません。

host ごとのネイティブ連携パッケージを使いたい場合は、まず [ネイティブ連携ガイド](../integrations/README.ja.md) を参照してください。

> **v0.21 — Gemini CLI → Antigravity 移行通知**: Gemini CLI は現在 **レガシー互換パス** として位置付けています。既存の Gemini CLI hook 設定はそのまま動作します。後継の Antigravity は v0.21.1 から Traceary のサポート対象 hook クライアントです（v0.21.0 は capability 診断のみ）。`traceary hooks install --client antigravity` で `hooks.json` を配線し、セッション開始とツール監査を記録します。`Stop` が読み取り可能な `transcriptPath` を渡す場合は、最終 turn の prompt / transcript event も記録します。現行の headless `agy --print` はこの `Stop` payload を発行します。最新状況は [Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) を参照してください。

## 含まれるファイル

- `scripts/hooks/*.sh`: **canonical** な互換ラッパー（`traceary hook ...` へ委譲）
- `examples/hooks/claude.settings.json`: Claude Code の設定例
- `examples/hooks/codex.hooks.json`: Codex CLI の設定例
- `examples/hooks/gemini.settings.json`: Gemini CLI の設定例
- `integrations/` と `plugins/` の配布パッケージには、bundle 形式の都合で必要な wrapper copy も含まれます

編集は `scripts/hooks/` のみ行い、続けて `go run ./cmd/repo-tooling integrations sync-hooks` を実行して Claude / Codex / Gemini / Grok パッケージのコピーをバイト一致に保ちます。コピーがドリフトすると `integrations verify`（CI）が失敗します。

新しく生成する hook 設定では、`~/.config/traceary/hook-scripts` へ portable script をインストールしません。新しい runtime 経路を使うには、hook 設定を再生成するか、配布済み integration package を使ってください。

## 前提条件

- `traceary` が `PATH` にある、または `--traceary-bin` / `TRACEARY_BIN` で使いたいバイナリを指定している
- `git` は任意です。使える場合は `remote.origin.url` を優先し、無いときはローカル Git worktree のルートを使い、それも無理な場合だけ hook の `cwd` にフォールバックします
- `bash` が必要なのは packaged compatibility wrapper を使う経路だけです
- 現在の hook 例は shell ベースの client を前提にしているため、Unix 系環境を想定しています
- Windows の PowerShell / `cmd.exe` 向け hook 実行はまだ正式対応していません。Windows で使う場合は、WSL などの POSIX 互換環境を利用してください
- generated hooks は、対象 client が外部 command 実行と、以下で説明する JSON payload / stdin の受け渡しに対応している前提です

## 共通環境変数

- `TRACEARY_BIN`: `traceary` バイナリの絶対パス。`PATH` に依存しないときに使います
- `TRACEARY_DB_PATH`: 既定の `~/.config/traceary/traceary.db` ではなく、明示した SQLite パスを使いたいときに指定します
- `TRACEARY_WORKSPACE`: 明示的な work-context 文字列。自動判定を上書きしたいときに使います
- `TRACEARY_HOOK_STATE_DIR`: 一時 session state の保存先を上書きします
- `TRACEARY_HOOK_STATE_KEY`: 既定の `PPID` ベース key が合わないときに、process ごとの state key を上書きします
- `TRACEARY_HOOK_DEBUG`: hook runtime のエラーは引き続き握りつぶしますが、抑止した error を stderr に出したいときに使います

## クライアントごとの差分

| Client | Settings file | Session start | Session end | Audit hook | Notes |
| --- | --- | --- | --- | --- | --- |
| Claude Code | `.claude/settings.json` or `~/.claude/settings.json` | `SessionStart` | `SessionEnd` | `PostToolUse` + `PostToolUseFailure` with `matcher: "Bash"` / `matcher: "mcp__.*"` / 組み込み tool matcher (`Read\|NotebookRead\|Edit\|MultiEdit\|Write\|NotebookEdit\|Grep\|Glob\|Agent\|Task\|TodoWrite\|WebFetch\|WebSearch\|ExitPlanMode`) | 現行 Anthropic docs では `Stop` は session-end hook ではなく per-response hook と定義されています |
| Codex CLI (`codex-cli 0.144.1`) | `~/.codex/hooks.json` | `SessionStart` | なし (MCP `manage_session` / stale GC) | `PostToolUse` | Codex CLI 0.144.1 では `SubagentStart` / `SubagentStop` と `PreCompact` / `PostCompact` も利用できる。`SessionEnd` はなく、`Stop` は assistant 応答ごとに発火するため、Traceary は turn 境界の transcript として扱い session 終了とはしません (#1170) |
| Gemini CLI (`gemini-cli 0.36.0`) *（レガシー互換）* | `.gemini/settings.json` or `~/.gemini/settings.json` | `SessionStart` | `SessionEnd` | `AfterTool` with `matcher: "run_shell_command"` | hook payload は JSON-over-stdin / JSON-over-stdout。`SessionEnd` は best-effort です。Gemini CLI はレガシーパスで、Antigravity が現役の後継です（v0.21.1 からサポート） |
| Antigravity (v0.21.1 からサポート) | `.agents/hooks.json` or `~/.gemini/config/hooks.json` | `PreInvocation`（`SessionStart` なし） | なし (MCP `manage_session` / stale GC) | `PreToolUse` + `PostToolUse` を `stepIdx` で突き合わせ（`run_command` のみ） | `hooks.json` は top-level の hook-group マップ（共有の `{"hooks": {...}}` 形式ではない）。`Stop` は execution 単位の turn 境界でセッション終了ではない (#1170)。詳細は [Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) |

## 何が記録されるか

### Session hooks

`traceary hook session <client> <start|end|stop>` は次の順で session ID を解決します。

1. hook input の `session_id`
2. `TRACEARY_HOOK_STATE_DIR` に保存済みの ID
3. `traceary session start` が新しく生成した ID

解決した session ID は process ごとの state file に保存されるため、client が毎回 `session_id` を送らない場合でも、後続の audit / prompt / compact hook で再利用できます。

### Audit hooks

`traceary hook audit <client>` は次を記録します。

- `command`: `tool_input.command`
- `input`: `tool_input` の compact JSON
- `output`: `tool_response` の compact JSON。失敗時は `{error, is_interrupt}`

`input` / `output` に含まれる secret らしい値は、SQLite に書き込む前に既定で伏せ字化されます。これは完全保証ではなく、ベストエフォートの保護です。

次の場合、hook は何も記録せず成功終了します。

- `traceary` が入っていない
- hook payload に `tool_input.command` が無い
- まだ session ID を解決できない
- `TRACEARY_NO_AUDIT` が truthy、または対象 tool が `traceary list --json`、`traceary sessions --snapshot --json`、MCP `list_events`、MCP `search` のような Traceary の read / self-inspection command

巨大な JSON を Traceary 自身の command audit に戻したくない ad-hoc 調査では `TRACEARY_NO_AUDIT=1` を付けてください。既知の read-only self-inspection surface は既定でも skip します。一方で、MCP `record_event` / `manage_memory` のような write 系 tool は引き続き audit 対象です。

### Prompt hooks

`traceary hook prompt <client>` は、ユーザーが送ったプロンプト本文を `prompt` イベントとして記録します。tool audit だけでは見えない「なぜその操作に至ったか」の判断履歴を残せます。現時点で配線されているのは:

- Claude Code の `UserPromptSubmit`
- Codex CLI (`codex-cli 0.121.0` 以降) の `UserPromptSubmit`

payload の `prompt` フィールドをそのまま記録し、redaction は適用しません。ユーザーの意図を正確に残すのが目的なので、サニタイズが必要な場合は Traceary の手前で行ってください。

次の場合、hook は何も記録せず成功終了します。

- payload に `prompt` フィールドが無い
- まだ session ID を解決できない

### SubagentStop (Claude Code 2026-01+)

`traceary hook subagent-stop claude` は Claude Code の `SubagentStop` フックで発火し、Task-tool の子エージェントが完了するたびに 1 回呼ばれます。`session_ended` イベントとして `[phase:subagent]` プレフィックス付きで保存し、`PostToolUse` 上の `agent_type` 推定に頼らずに明示的な subagent ライフサイクル境界を記録できます。

### PreCompact (Claude Code 2026-01+)

`traceary hook compact claude pre-compact` は Claude Code の `PreCompact` フックで発火し、会話が実際に圧縮される前に呼ばれます。`compact_summary` イベントを `[phase:pre-compact]` プレフィックス付きで保存し、replay / 振り返りで「圧縮前スナップショット」と「圧縮後サマリ」を区別できます。`loadCompactSummary` はこのプレフィックスを skip するため、キャンセルされた compact サイクルが pre-compact スナップショットを最新行として残しても `session_handoff` / `memory_pack` は引き続き正しい post-compact summary を返します。

### Codex の compact / subagent hook（Codex CLI 0.144.1 以降）

`PreCompact` / `PostCompact` は対応する phase で `traceary hook compact codex` を呼び出します。Codex が渡すのは `trigger`（`manual` または `auto`）だけで圧縮後サマリー本文は含まれないため、Traceary は phase 別の `compact_summary` 境界 marker として保存します。`SubagentStart` / `SubagentStop` は共通の child session runtime を呼び出し、`agent_id` を対応付け key、`agent_type` を child agent 名として使用します。

## 導入フロー

### CLI で設定を出力する

`traceary hooks print --client <claude|codex|gemini>` は、貼り付け用の config を出力します。`claude-code`, `codex-cli`, `gemini-cli` も alias として使えます。

まず install / check / verify の流れだけ確認したい場合は、`traceary hooks guide --client <claude|codex|gemini>` を使ってください。

例:

- `traceary hooks print --client claude > .claude/settings.json`
- `traceary hooks print --client codex > ~/.codex/hooks.json`
- `traceary hooks print --client gemini > .gemini/settings.json`

既定では生成コマンドが `'traceary' 'hook' ...` を呼ぶため、hook は `PATH` 上の安定した `traceary` コマンドを参照します。

特定のバイナリパスに固定したい場合は `--traceary-bin` を使います。

### 標準パスに設定を書き出す

`traceary hooks install --client <claude|codex|gemini>` は、生成した config を Traceary 側で標準パスに書き出します。`claude-code`, `codex-cli`, `gemini-cli` も alias として使えます。

例:

- `traceary hooks install --client claude`
- `traceary hooks install --client codex`
- `traceary hooks install --client gemini`

既定の出力先:

- Claude: `<project>/.claude/settings.json`
- Codex: `~/.codex/hooks.json`
- Gemini: `<project>/.gemini/settings.json`

### Claude の PostToolUse matcher preset (`--matcher`)

Claude client に限り、`hooks install` / `hooks print` で `--matcher <preset>` が使えます。`PostToolUse` / `PostToolUseFailure` の対象を切り替えます。

- `minimal` — `Bash` + `mcp__.*` のみ。v0.8-6 以前の Traceary と同じセットです。built-in tool の監査が `tail` / `timeline` のノイズになり過ぎるときに選びます。
- `default`（`--matcher` を省略したとき） — v0.8-6b で導入した built-in tool 列 (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`) を含みます。配布 plugin も同じセットです。
- `all` — built-in 列の代わりに `.*` を適用し、すべての tool を拾います。plugin 独自 tool やプロジェクト固有 tool も含まれるため、明示的にオプトインする用途向けです。

`codex` / `gemini` client は元から全 tool を監査するため、このフラグは無視されます。

Claude Code plugin が有効な場合、`hooks install --client claude` は `--matcher` の値に関わらず skip されます (二重記録回避)。plugin 配布の `hooks.json` は default matcher で固定なので、preset を切り替えたい場合は plugin を無効化してから `hooks install --matcher <preset>` を実行してください。

### user-level へのインストール (`--global`)

`--global` を指定するとプロジェクト配下ではなく user-level の設定に書き込みます。

- Claude: `~/.claude/settings.json`
- Gemini: `~/.gemini/settings.json`
- Codex: 元から user-level のため `--global` は no-op (従来通り `~/.codex/hooks.json` を使用)

`--global` と `--output` は排他です。user-level hooks はマシン上の全プロジェクトに適用されるため、複数のリポジトリで Traceary を使うがプロジェクトごとに `.claude/settings.json` を commit したくない場合に適しています。

既存ファイルがある場合、Traceary は上書きせずエラーにします。まず差分を確認し、本当に置き換えてよいときだけ `--force` を使ってください。
対応している JSON config であれば、`hooks install` は既存設定へ Traceary 管理下の hook entry をマージし、無関係な設定は保持します。`--force` を付けた場合だけ完全上書きします。

### 非破壊マイグレーション (`--upgrade`)

Traceary のリリースで新しい hook event (例: `UserPromptSubmit`) が追加されたとき、ユーザー追加の hook を触らずに新規分だけ反映したい場合は `--upgrade` を使います。

```
traceary hooks install --client codex --upgrade
```

`--upgrade` はデフォルト install と同じ merge パスを明示的に実行し、さらに以下を保証します。

- 既存ファイルを決して上書きしない (`--force` とは排他)
- ユーザー追加の非 Traceary hook はそのまま残す
- Traceary 管理分のみ最新の定義で置き換える (バイナリパス変更や script 形式 → 直接呼び出し形式へのリライトも対象)
- 現行リリースで廃止された event に残っている Traceary 管理 entry は削除し、稼働中のバイナリと Traceary フットプリントを一致させる (`削除` として表示)
- イベント単位のサマリを表示 (`追加: UserPromptSubmit`、`更新: ...`、`削除: ...`、`変更なし: ...`)
- idempotent — アップグレード後に再実行すると「既に最新」と表示し、ファイルはバイト同一で維持される

`hooks install` の実行後には、そのまま確認に使える `doctor` コマンドも出力します。

**Claude Code plugin との関係**: Traceary の Claude Code plugin が有効な場合（`~/.claude/settings.json` の `enabledPlugins` で検知）、`hooks install --client claude` は settings file への書き込みをスキップして通知を出します。plugin がすでに同じ hook を Claude Code に提供しているため、重ねて install すると audit が 1 ツール呼び出しにつき 2 回記録されてしまいます。両方に登録したい場合（plugin 開発など）のみ `--force` を使ってください。

### merge の条件と失敗条件

`hooks install` が既存ファイルへマージできるのは、次をすべて満たす場合です。

- destination file がすでに存在する
- root が JSON object である
- 既存 `hooks` field が未設定か、Traceary が期待する `map[string][]hookMatcher` の形になっている

次の場合は、推測で書き換えずに失敗します。

- 既存 file が valid JSON ではない
- JSON root が object ではない
- 既存 `hooks` field の shape が Traceary の想定と違う

その場合はファイルを自分で確認し、本当に置き換えてよいときだけ `--force` を使ってください。

### timeout kill 時の保全

すべての非公開 `traceary hook ...` entrypoint は、SQLite に触れる前に host payload を `~/.config/traceary/hooks/spool/` 配下の mode `0600` record へ保存します。record は atomic に公開され、hook operation が成功した後だけ削除されます。SIGTERM は command context を cancel します。host が競合中または低速な hook を kill しても、先に公開した record が残るため event は黙って消えません。`traceary doctor` は残存 record と読めない record を `hook-spool` check で報告します。残存 record は payload を確認し、対応する command を再実行してから削除してください。経過時間だけでは自動削除しません。

SQLite が writer 競合を待つのは最大1秒です。packaged host hook の budget は必ずこの待機時間を上回る必要があります。Gemini の生成・同梱 hook timeout は10秒で、DB attempt が失敗して spool record を保持したまま host deadline より前に制御を戻せます。この関係は意図的です: `SQLite busy_timeout < host hook timeout`。

### メモリ抽出キュー

session end、turn boundary、subagent stop の hook は、主要な event を先に commit してから、メモリ自動抽出を `~/.config/traceary/hooks/memory-extract/` に enqueue します。job に入るのは抽出条件と運用 metadata だけで、mode は `0600` です。同じ database・session・workspace への繰り返し要求は1件にまとめます。host hook が終了した後、分離した内部 worker が既存の extractor を呼び出します。成功時は job を削除し、失敗または中断された場合は再試行できる状態で残します。これにより、抽出処理は host hook timeout を消費せず、主要 event の commit を遅らせません。

`traceary doctor` は `hook-memory-extract` check で、未処理件数、以前失敗した件数、読めない件数、最古の経過時間を報告します。抽出内容や保存された条件は表示しません。次に同じ対象の hook 要求が来ると worker が再起動します。警告が残り続ける場合は debug log を確認してください。

## トラブルシュート

hooks やローカル SQLite ストアの挙動がおかしいときは `traceary doctor --client <claude|codex|gemini|antigravity|grok>` を実行してください。Antigravity と Grok は既定の client 一覧に含まれないため、明示的な指定が必要です。

この診断コマンドでは次を確認します。

- DB path の解決と store 初期化可否
- 想定される client config path と、そこに Traceary 管理下の hook が入っているか
- Traceary の optional config（追加 redaction pattern など）が壊れていないか
- Claude の場合、global `~/.claude/settings.json` で Traceary plugin が有効かどうか、および project の settings と併存していないか（併存時は二重記録になるため `warn`）

hooks 未導入などの初回状態では `warn` が出るのが自然です。たとえば host 側 config file がまだ無い場合は `fail` ではなく `warn` になります。
`fail` は、DB アクセス不良、config 読み込み失敗、invalid な config shape のような「壊れている状態」に限って使います。

`doctor` は対象 client 自体を起動するわけではありません。file path、DB access、Traceary 管理下の hook entry の存在までは確認できますが、第三者 client が検証時と同じ形で hook を発火することまでは保証しません。

SQLite concurrency の前提、PPID ベース hook state の注意点、その他の既知の運用前提は [`../operations/README.ja.md`](../operations/README.ja.md) を参照してください。

### Claude Code

1. `examples/hooks/claude.settings.json` を `.claude/settings.json` にコピーし、既存設定とマージする
2. `PATH` に依存しない場合は、生成された config が使いたい Traceary バイナリを指していることを確認する
3. project で Claude Code を起動する
4. 短い session のあと `traceary list --limit 10` を実行し、`session_started`, `session_ended`, `command_executed`, `compact_summary`, `prompt` が入っていることを確認する

### Codex CLI

1. `examples/hooks/codex.hooks.json` を `~/.codex/hooks.json` にコピーする
2. `traceary` が `PATH` に無いなら、`--traceary-bin` 付きで config を再生成して絶対パスへ固定する
3. Codex session を開始し、`traceary list --limit 10` を確認する

Codex の `Stop` は assistant 応答ごとに発火するため、Traceary は turn 境界の transcript として記録し session は開いたままにします (#1170)。Codex session は明示的な終了 (MCP `manage_session`) または activity-aware stale GC で終了します。GC は通常の hook start 後にデータベースごと最大 6 時間に 1 回自動実行され、`traceary session gc` は手動・定期実行のフォールバックとして利用できます。

### Gemini CLI *（レガシー互換）*

> Gemini CLI はレガシー hook パスです。後継の Antigravity は v0.21.1 からサポートされています（下記参照）。

1. `examples/hooks/gemini.settings.json` を `.gemini/settings.json` または `~/.gemini/settings.json` にマージする
2. `hooksConfig.enabled` が既に `true` になっていることを確認する。Traceary はここを自動変更しません
3. Gemini CLI を起動して、少なくとも 1 回 shell command を実行する
4. `traceary list --limit 10` または `traceary search "<command>"` で記録結果を確認する

### Antigravity

1. Traceary hook をインストールする: `traceary hooks install --client antigravity --project-dir .`（workspace は `.agents/hooks.json`）または `--global`（`~/.gemini/config/hooks.json`）。`agy` / `antigravity-cli` alias も使えます
2. Antigravity の conversation を開始し、少なくとも 1 回 `run_command` tool を実行する
3. `traceary list --limit 10` で記録結果を確認する
4. `traceary doctor --client antigravity --json` で install を確認する。doctor は `antigravity-capability` に加えて install 経路ごとの check（`antigravity-hooks-workspace`・`antigravity-hooks-user`・`antigravity-cli-plugin`）と集約サマリー `antigravity-hooks` を報告します。各経路は任意で、user-level または CLI plugin の経路が健全なら、存在しない workspace `.agents/hooks.json` は `warn` ではなく `skip` 扱いです。doctor が warn するのは、どの経路も `traceary` グループを登録していないときだけです

Antigravity に `SessionStart` はなく、Traceary は `PreInvocation` から session を冪等に開始します。Codex 同様 `Stop` は execution 単位の turn 境界なので session は開いたままで、MCP `manage_session` または stale GC で終了します。audit 対象は `run_command` tool のみです（`PreToolUse` の command args と `PostToolUse` の結果を突き合わせます）。詳細は [Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) を参照してください。

Traceary CLI が失敗したときの stderr は plain `Error: ...` です。wrapper 経由の hook 実行でも、exit code と stderr text をそのまま扱えます。

## 参考

- [ライフサイクルイベント](./lifecycle-events.ja.md) — hook が発行する canonical Traceary event kind 一覧。
- Claude Code hooks reference: https://code.claude.com/docs/en/hooks
- Claude Code hooks guide: https://code.claude.com/docs/en/hooks-guide
- Gemini CLI hooks reference used during local validation: `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`
