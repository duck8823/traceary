# Hooks ガイド

[English](./README.md)

Traceary は、Claude Code / Codex CLI / Gemini CLI から送られる hook イベントを、隠しサブコマンド `traceary hook ...` で受け取って session 境界、command audit、compact summary、prompt を記録します。

生成される hook 設定と配布済みの host package は、この Go runtime entrypoint を直接呼ぶ前提です。`scripts/hooks/` 配下の shell script は、配布物や既存環境との互換性を保つための薄いラッパーとして残していますが、主 runtime 実装ではありません。

生成済みの hook 設定は、対応しているクライアント設定ファイルであれば既存 JSON にマージして追加できます。既定では、既存設定を破壊的に置き換えません。

host ごとのネイティブ連携パッケージを使いたい場合は、まず [ネイティブ連携ガイド](../integrations/README.ja.md) を参照してください。

## 含まれるファイル

- `scripts/hooks/*.sh`: `traceary hook ...` へ委譲する互換ラッパー
- `examples/hooks/claude.settings.json`: Claude Code の設定例
- `examples/hooks/codex.hooks.json`: Codex CLI の設定例
- `examples/hooks/gemini.settings.json`: Gemini CLI の設定例
- `integrations/` と `plugins/` の配布パッケージには、bundle 形式の都合で必要な wrapper copy も含まれます

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
| Claude Code | `.claude/settings.json` or `~/.claude/settings.json` | `SessionStart` | `SessionEnd` | `PostToolUse` + `PostToolUseFailure` with `matcher: "Bash"` と `matcher: "mcp__.*"` | 現行 Anthropic docs では `Stop` は session-end hook ではなく per-response hook と定義されています |
| Codex CLI (`codex-cli 0.118.0`) | `~/.codex/hooks.json` | `SessionStart` | `Stop` (best effort) | `PostToolUse` | ローカルの Codex build では `SessionEnd` が見つからず、`Stop` を best-effort で使います |
| Gemini CLI (`gemini-cli 0.36.0`) | `.gemini/settings.json` or `~/.gemini/settings.json` | `SessionStart` | `SessionEnd` | `AfterTool` with `matcher: "run_shell_command"` | hook payload は JSON-over-stdin / JSON-over-stdout。`SessionEnd` は best-effort です |

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

### Prompt hooks

`traceary hook prompt <client>` は、ユーザーが送ったプロンプト本文を `prompt` イベントとして記録します。tool audit だけでは見えない「なぜその操作に至ったか」の判断履歴を残せます。現時点で配線されているのは:

- Claude Code の `UserPromptSubmit`
- Codex CLI (`codex-cli 0.121.0` 以降) の `UserPromptSubmit`

payload の `prompt` フィールドをそのまま記録し、redaction は適用しません。ユーザーの意図を正確に残すのが目的なので、サニタイズが必要な場合は Traceary の手前で行ってください。

次の場合、hook は何も記録せず成功終了します。

- payload に `prompt` フィールドが無い
- まだ session ID を解決できない

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

## トラブルシュート

hooks やローカル SQLite ストアの挙動がおかしいときは `traceary doctor --client <claude|codex|gemini>` を実行してください。

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
4. installed Codex build で `Stop` が per-turn だと分かった場合は、session-start hook は維持しつつ、stop hook はベストエフォートとして扱う

Codex の session end capture は意図的にベストエフォート扱いです。installed build が `Stop` を出している間は stop event を記録できますが、Claude Code や Gemini CLI と同じ精度は保証しません。

### Gemini CLI

1. `examples/hooks/gemini.settings.json` を `.gemini/settings.json` または `~/.gemini/settings.json` にマージする
2. `hooksConfig.enabled` が既に `true` になっていることを確認する。Traceary はここを自動変更しません
3. Gemini CLI を起動して、少なくとも 1 回 shell command を実行する
4. `traceary list --limit 10` または `traceary search "<command>"` で記録結果を確認する

Traceary CLI が失敗したときの stderr は plain `Error: ...` です。wrapper 経由の hook 実行でも、exit code と stderr text をそのまま扱えます。

## 参考

- Claude Code hooks reference: https://code.claude.com/docs/en/hooks
- Claude Code hooks guide: https://code.claude.com/docs/en/hooks-guide
- Gemini CLI hooks reference used during local validation: `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`
