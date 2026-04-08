# Hooks integration

[English](./README.md)

Traceary v0.1 は、既存の `traceary session ...` / `traceary audit ...` を hook script から呼び出すことで、Claude Code / Codex CLI / Gemini CLI の session 境界と shell command audit を取り込めます。

現在の generated hook config は、対応している client 設定ファイルであれば既存 JSON にマージしながら Traceary hook を追加できます。既定で破壊的な置き換えはしません。

## ファイル

- `scripts/hooks/traceary-session.sh`: session start/end 境界を記録
- `scripts/hooks/traceary-audit.sh`: tool 実行後の shell command audit を記録
- `examples/hooks/claude.settings.json`: Claude Code の例
- `examples/hooks/codex.hooks.json`: Codex CLI の例
- `examples/hooks/gemini.settings.json`: Gemini CLI の例

`traceary hooks print/install` は、既定ではこれらの portable copy も `~/.config/traceary/hook-scripts` 配下へ書き出します。installed binary でも source checkout 前提になりません。

## 前提条件

- `traceary` が `PATH` にある、または `TRACEARY_BIN` が binary を指している
- `git` は任意。ある場合は `remote.origin.url` を Traceary の `repo` field に正規化し、無い場合は hook の `cwd` を使う
- 現在の hook 例は shell ベースの client を前提にしているため、Unix 系環境を想定しています

## 共通環境変数

- `TRACEARY_BIN`: `traceary` binary の絶対 path
- `TRACEARY_DB_PATH`: 既定の `~/.config/traceary/traceary.db` ではなく明示的な SQLite path を使いたいときに指定
- `TRACEARY_REPO`: 明示的な work-context 文字列。auto-detection を上書きしたいときに使う
- `TRACEARY_HOOK_SCRIPTS_DIR`: `traceary hooks print/install` が portable hook script を書き出す先を上書き
- `TRACEARY_HOOK_STATE_DIR`: 一時 session state の保存先を上書き
- `TRACEARY_HOOK_STATE_KEY`: 既定の `PPID` ベース key が合わないときに process ごとの state key を上書き

## クライアント差分

| Client | Settings file | Session start | Session end | Audit hook | Notes |
| --- | --- | --- | --- | --- | --- |
| Claude Code | `.claude/settings.json` or `~/.claude/settings.json` | `SessionStart` | `SessionEnd` | `PostToolUse` + `PostToolUseFailure` with `matcher: "Bash"` | 現行 Anthropic docs では `Stop` は session-end hook ではなく per-response hook と定義されている |
| Codex CLI (`codex-cli 0.118.0`) | `~/.codex/hooks.json` | `SessionStart` | `Stop` (best effort) | `PostToolUse` | ローカルの Codex build の strings では `SessionEnd` は見つからず、`Stop` を best-effort で使う |
| Gemini CLI (`gemini-cli 0.36.0`) | `.gemini/settings.json` or `~/.gemini/settings.json` | `SessionStart` | `SessionEnd` | `AfterTool` with `matcher: "run_shell_command"` | hook payload は JSON-over-stdin / JSON-over-stdout。`SessionEnd` は best-effort |

## 何が記録されるか

### Session hooks

`traceary-session.sh` は次の順で session ID を解決します。

1. hook input の `session_id`
2. `TRACEARY_HOOK_STATE_DIR` に保存済みの ID
3. `traceary session start` が新しく生成した ID

解決した session ID は process ごとの state file に保存されるので、client が毎回 `session_id` を送らない場合でも、後続の audit hook が再利用できます。

### Audit hooks

`traceary-audit.sh` は次を記録します。

- `command`: `tool_input.command`
- `input`: `tool_input` の compact JSON
- `output`: `tool_response` の compact JSON、失敗時は `{error, is_interrupt}`

`input` / `output` に含まれる secret っぽい値は、SQLite に書き込む前に既定で伏せ字化されます。これは完全保証ではなく、best-effort の保護です。

次の場合、script は何も記録せず成功終了します。

- `traceary` が入っていない
- hook payload に `tool_input.command` が無い
- まだ session ID を解決できない

## 導入フロー

### CLI で設定を生成する

`traceary hooks print --client <claude|codex|gemini>` は、貼り付け用の config を出力します。`claude-code`, `codex-cli`, `gemini-cli` も alias として使えます。

例:

- `traceary hooks print --client claude > .claude/settings.json`
- `traceary hooks print --client codex > ~/.codex/hooks.json`
- `traceary hooks print --client gemini > .gemini/settings.json`

既定では生成コマンドに `TRACEARY_BIN='traceary'` を使うため、hook は `PATH` 上の安定した `traceary` command を追従します。

最初の `hooks print/install` 実行時に、portable script も `~/.config/traceary/hook-scripts`（または `TRACEARY_HOOK_SCRIPTS_DIR`）へ書き出します。生成される config は `<project>/scripts/hooks/...` ではなく、その安定した directory を参照するため、source checkout の外にある installed Traceary binary でも動きます。

特定の binary path に pin したいときは `--traceary-bin` を使います。

### 標準 path に設定を書き出す

`traceary hooks install --client <claude|codex|gemini>` は、生成した config file を Traceary 側で標準 path に書き出します。`claude-code`, `codex-cli`, `gemini-cli` も alias として使えます。

例:

- `traceary hooks install --client claude`
- `traceary hooks install --client codex`
- `traceary hooks install --client gemini`

既定の出力先:

- Claude: `<project>/.claude/settings.json`
- Codex: `~/.codex/hooks.json`
- Gemini: `<project>/.gemini/settings.json`

既存 file がある場合、Traceary は上書きせずエラーにします。まず差分を確認し、既存 file を置き換える意図があるときだけ `--force` を使ってください。
対応している JSON config であれば、`hooks install` はまず既存設定へ Traceary 管理下の hook entry をマージし、無関係な設定は保持します。`--force` を付けた場合だけ完全上書きします。

## トラブルシューティング

hooks やローカル SQLite store の挙動がおかしいときは `traceary doctor --client <claude|codex|gemini>` を実行してください。

この診断コマンドでは次を確認します。

- DB path の解決と store 初期化可否
- hook script の materialize と executable 権限
- 想定される client config path と、そこに Traceary 管理下の hook が入っているか

### Claude Code

1. `examples/hooks/claude.settings.json` を `.claude/settings.json` にコピーし、既存設定と merge する
2. `PATH` に依存しない場合は、生成された config が使いたい Traceary binary を指していることを確認する
3. project で Claude Code を起動する
4. 短い session のあと `traceary list --limit 10` を実行し、`session_started`, `session_ended`, `command_executed` が入っていることを確認する

### Codex CLI

1. `examples/hooks/codex.hooks.json` を `~/.codex/hooks.json` にコピーし、`/absolute/path/to/traceary` を置き換える
2. `traceary` が `PATH` に無いなら `TRACEARY_BIN` を export する
3. Codex session を開始し、`traceary list --limit 10` を確認する
4. installed Codex build で `Stop` が per-turn だと分かった場合は、session-start hook は維持しつつ stop hook は best-effort として扱う

### Gemini CLI

1. `examples/hooks/gemini.settings.json` を `.gemini/settings.json` または `~/.gemini/settings.json` に merge する
2. `hooksConfig.enabled` が既に `true` になっていることを確認する
3. Gemini CLI を起動して、少なくとも 1 回 shell command を実行する
4. `traceary list --limit 10` または `traceary search "<command>"` で記録結果を確認する

Traceary CLI が失敗したときの stderr は plain `Error: ...` です。hook script は structured JSON log を剥がさずに、exit code と stderr text をそのまま扱えます。

## 参考

- Claude Code hooks reference: https://code.claude.com/docs/en/hooks
- Claude Code hooks guide: https://code.claude.com/docs/en/hooks-guide
- Gemini CLI hooks reference used during local validation: `/opt/homebrew/Cellar/gemini-cli/0.36.0/libexec/lib/node_modules/@google/gemini-cli/bundle/docs/hooks/reference.md`
