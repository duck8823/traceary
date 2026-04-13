# Hooks ガイド

[English](./README.md)

Traceary v0.1 では、`traceary session ...` と `traceary audit ...` を hook スクリプトから呼び出すことで、Claude Code / Codex CLI / Gemini CLI のセッション境界とシェルコマンド監査を取り込めます。

生成済みの hook 設定は、対応しているクライアント設定ファイルであれば既存 JSON にマージして追加できます。既定では、既存設定を破壊的に置き換えません。

host ごとのネイティブ連携パッケージを使いたい場合は、まず [ネイティブ連携ガイド](../integrations/README.ja.md) を参照してください。

## 含まれるファイル

- `scripts/hooks/traceary-session.sh`: session start/end を記録
- `scripts/hooks/traceary-audit.sh`: tool 実行後の shell command audit を記録
- `examples/hooks/claude.settings.json`: Claude Code の設定例
- `examples/hooks/codex.hooks.json`: Codex CLI の設定例
- `examples/hooks/gemini.settings.json`: Gemini CLI の設定例

`traceary hooks print/install` は、既定でこれらの実行用コピーも `~/.config/traceary/hook-scripts` 配下へ書き出します。インストール済みバイナリから使う場合でも、ソースチェックアウトに依存しません。

## 前提条件

- `traceary` が `PATH` にある、または `TRACEARY_BIN` がバイナリを指している
- `git` は任意です。使える場合は `remote.origin.url` を優先し、無いときはローカル Git worktree のルートを使います。`git` 自体が使えない場合だけ hook の `cwd` にフォールバックします
- 生成される実行用スクリプトは `#!/usr/bin/env bash` を使うため、`bash` が必要です
- 現在の hook 例は shell ベースの client を前提にしているため、Unix 系環境を想定しています
- Windows の PowerShell / `cmd.exe` 向け hook 実行はまだ正式対応していません。Windows で使う場合は、WSL などの POSIX 互換環境を利用してください
- generated hooks は、対象 client が外部 command 実行と、以下で説明する JSON payload / stdin の受け渡しに対応している前提です

## 共通環境変数

- `TRACEARY_BIN`: `traceary` バイナリの絶対パス
- `TRACEARY_DB_PATH`: 既定の `~/.config/traceary/traceary.db` ではなく、明示した SQLite パスを使いたいときに指定
- `TRACEARY_WORKSPACE`: 明示的な work-context 文字列。自動判定を上書きしたいときに使う
- `TRACEARY_HOOK_SCRIPTS_DIR`: `traceary hooks print/install` が実行用 hook script を書き出す先を上書き
- `TRACEARY_HOOK_STATE_DIR`: 一時 session state の保存先を上書き
- `TRACEARY_HOOK_STATE_KEY`: 既定の `PPID` ベース key が合わないときに、process ごとの state key を上書き

## クライアントごとの差分

| Client | Settings file | Session start | Session end | Audit hook | Notes |
| --- | --- | --- | --- | --- | --- |
| Claude Code | `.claude/settings.json` or `~/.claude/settings.json` | `SessionStart` | `SessionEnd` | `PostToolUse` + `PostToolUseFailure` with `matcher: "Bash"` | 現行 Anthropic docs では `Stop` は session-end hook ではなく per-response hook と定義されている |
| Codex CLI (`codex-cli 0.118.0`) | `~/.codex/hooks.json` | `SessionStart` | `Stop` (best effort) | `PostToolUse` | ローカルの Codex build では `SessionEnd` が見つからず、`Stop` を best-effort で使う |
| Gemini CLI (`gemini-cli 0.36.0`) | `.gemini/settings.json` or `~/.gemini/settings.json` | `SessionStart` | `SessionEnd` | `AfterTool` with `matcher: "run_shell_command"` | hook payload は JSON-over-stdin / JSON-over-stdout。`SessionEnd` は best-effort |

## 何が記録されるか

### Session hooks

`traceary-session.sh` は次の順で session ID を解決します。

1. hook input の `session_id`
2. `TRACEARY_HOOK_STATE_DIR` に保存済みの ID
3. `traceary session start` が新しく生成した ID

解決した session ID は process ごとの state file に保存されるため、client が毎回 `session_id` を送らない場合でも、後続の audit hook で再利用できます。

### Audit hooks

`traceary-audit.sh` は次を記録します。

- `command`: `tool_input.command`
- `input`: `tool_input` の compact JSON
- `output`: `tool_response` の compact JSON。失敗時は `{error, is_interrupt}`

`input` / `output` に含まれる secret らしい値は、SQLite に書き込む前に既定で伏せ字化されます。これは完全保証ではなく、ベストエフォートの保護です。

次の場合、script は何も記録せず成功終了します。

- `traceary` が入っていない
- hook payload に `tool_input.command` が無い
- まだ session ID を解決できない

## 導入フロー

### CLI で設定を出力する

`traceary hooks print --client <claude|codex|gemini>` は、貼り付け用の config を出力します。`claude-code`, `codex-cli`, `gemini-cli` も alias として使えます。

まず install / check / verify の流れだけ確認したい場合は、`traceary hooks guide --client <claude|codex|gemini>` を使ってください。

例:

- `traceary hooks print --client claude > .claude/settings.json`
- `traceary hooks print --client codex > ~/.codex/hooks.json`
- `traceary hooks print --client gemini > .gemini/settings.json`

既定では生成コマンドに `TRACEARY_BIN='traceary'` を使うため、hook は `PATH` 上の `traceary` コマンドを参照します。

最初の `hooks print/install` 実行時に、実行用スクリプトも `~/.config/traceary/hook-scripts`（または `TRACEARY_HOOK_SCRIPTS_DIR`）へ書き出します。生成される設定は `<project>/scripts/hooks/...` ではなく、この安定したディレクトリを参照するため、インストール済み Traceary バイナリだけでも動きます。

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

既存ファイルがある場合、Traceary は上書きせずエラーにします。まず差分を確認し、本当に置き換えてよいときだけ `--force` を使ってください。
対応している JSON config であれば、`hooks install` は既存設定へ Traceary 管理下の hook entry をマージし、無関係な設定は保持します。`--force` を付けた場合だけ完全上書きします。
`hooks install` の実行後には、そのまま確認に使える `doctor` コマンドも出力します。

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
- hook script の展開状況と実行権限
- 想定される client config path と、そこに Traceary 管理下の hook が入っているか

hooks 未導入などの初回状態では `warn` が出るのが自然です。たとえば host 側 config file がまだ無い場合は `fail` ではなく `warn` になります。
`fail` は、DB アクセス不良、config 読み込み失敗、invalid な config shape のような「壊れている状態」に限って使います。

`doctor` は対象 client 自体を起動するわけではありません。file path、DB access、Traceary 管理下の hook entry の存在までは確認できますが、第三者 client が検証時と同じ形で hook を発火することまでは保証しません。

SQLite concurrency の前提、PPID ベース hook state の注意点、その他の既知の運用前提は [`../operations/README.ja.md`](../operations/README.ja.md) を参照してください。

### Claude Code

1. `examples/hooks/claude.settings.json` を `.claude/settings.json` にコピーし、既存設定とマージする
2. `PATH` に依存しない場合は、生成された config が使いたい Traceary バイナリを指していることを確認する
3. project で Claude Code を起動する
4. 短い session のあと `traceary list --limit 10` を実行し、`session_started`, `session_ended`, `command_executed` が入っていることを確認する

### Codex CLI

1. `examples/hooks/codex.hooks.json` を `~/.codex/hooks.json` にコピーし、`/absolute/path/to/traceary` を置き換える
2. `traceary` が `PATH` に無いなら `TRACEARY_BIN` を export する
3. Codex session を開始し、`traceary list --limit 10` を確認する
4. installed Codex build で `Stop` が per-turn だと分かった場合は、session-start hook は維持しつつ、stop hook はベストエフォートとして扱う

Codex の session end capture は意図的にベストエフォート扱いです。installed build が `Stop` を出している間は stop event を記録できますが、Claude Code や Gemini CLI と同じ精度は保証しません。

### Gemini CLI

1. `examples/hooks/gemini.settings.json` を `.gemini/settings.json` または `~/.gemini/settings.json` にマージする
2. `hooksConfig.enabled` が既に `true` になっていることを確認する。Traceary はここを自動変更しません
3. Gemini CLI を起動して、少なくとも 1 回 shell command を実行する
4. `traceary list --limit 10` または `traceary search "<command>"` で記録結果を確認する

Traceary CLI が失敗したときの stderr は plain `Error: ...` です。hook script は structured JSON log を剥がさずに、exit code と stderr text をそのまま扱えます。

## 参考

- Claude Code hooks reference: https://code.claude.com/docs/en/hooks
- Claude Code hooks guide: https://code.claude.com/docs/en/hooks-guide
- Gemini CLI hooks reference used during local validation: `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`
