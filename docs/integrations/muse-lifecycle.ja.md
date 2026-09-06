# Muse Code ライフサイクル（調査）

[English](./muse-lifecycle.md)

[#2350](https://github.com/duck8823/traceary/issues/2350)（`v0.50.0-1`）の調査記録です。
本イシューは **証拠付きの調査のみ** です。Muse plugin 実装と host-coverage
matrix の更新は行いません（`v0.50.0-2` / `v0.50.0-3`）。

ピン留め CLI（2026-09-06）:

| 項目 | 観測 |
| --- | --- |
| `muse --version` | `Muse Code 1.0.3 (1.0.3-R2198.1)` |
| ランチャー | `/Users/duck8823/.local/bin/muse`（bash wrapper、`channel=muse-stable`） |
| 実行バイナリ | `/Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1`（`Mach-O 64-bit executable arm64`） |
| データ | `~/.local/share/muse/` |
| 設定 | `~/.config/muse/` |

コマンド:

```sh
muse --version
ls -la "$(command -v muse)"
ls -la /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
file /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
```

---

## 1. セッションライフサイクル

### 開始

- 対話: `muse [OPTIONS] [PROMPT]` は TUI（`muse --help`: 「サブコマンドなしなら
  interactive TUI」）。
- Headless: `muse exec [OPTIONS] [PROMPT]` は **プロンプト 1 回** で終了
  （`muse exec --help`: 「Run one prompt non-interactively (headless)」）。
- `muse exec --json` は stdout に MSP JSONL。echo provider の観測
  （`--provider echo --no-session-log`）:
  `runtime.command.accepted`, `session.run.linked`,
  `session.workspace_branch.observed`, `turn.input.user`,
  `run.lifecycle.started`, `task.stream.linked`, `task.lifecycle.*`,
  `run.output.delta`, `run.terminal.completed`。終了コード `0`。
- `--no-session-log` を付けない場合、耐久ログに
  `session.opened.observed`（`resume: false`）、
  `session.startup_phases.observed`、後に `session.end` が残る。
  例: `~/.local/share/muse/sessions/2026/09/03/01a067b6-81b6-7490-91b0-494857e673a1/session.jsonl`
  - `session.opened.observed` → `resume: false`, `security_mode: "normal"`
  - `session.end` → `exit_reason: "clean"`, `uptime_ms`, `session_log_bytes`

`--no-session-log` はディスクへ書かず、session messaging も無効:

```
muse: local session messaging disabled: session logging is required
```

（echo `exec` probe、2026-09-06。）

### 永続化

レイアウト（読み取り専用）:

```
~/.local/share/muse/sessions/YYYY/MM/DD/<session-id>/
  session.jsonl          # durable event log
  cli-*.log              # per-process CLI log
  cron.db[+shm|+wal]     # session-local cron
  session.peer-history.sqlite3
  tool-outputs/.spool/   # tool output spool (e.g. call_*-bash.txt.tmp)
  approval-review/       # often empty until a review artifact is written
  .session.lock
```

索引: `~/.local/share/muse/session-index.db` の `sessions` テーブル
（`session_id`, `session_dir`, `session_log_path`, `workspace_root`, `status`,
`layout`, titles, timestamps）。

完了した `muse exec`（meta provider、`--approval-mode on-request`、
session `01a07613-1043-75a2-b471-661929150803`）は
`sessions/2026/09/06/<id>/session.jsonl`（95 行）と
`sessions/.msp-view-v1/<id>` を書いた。

### 再開

```
muse resume            # session picker for this workspace
muse resume --last     # most recent session in this workspace
muse resume <uuid>
```

（`muse resume --help`。）再開は **対話 TUI** であり headless 一発実行ではない。
耐久ログに `session.resumed`（例: `prior_turn_count: 3`,
`resumed_from_sequence: 811`、session
`01a0692b-a8e2-7530-8729-a67260ea9f19`）。同じ resume サンプルの
`session.started` は `"background_tasks": "kill"` と
`previous_session_stream`。`"background_tasks": "keep"` は **別** セッション
（`01a067b6-81b6-7490-91b0-494857e673a1`、2026/09/03）で観測され、
resume サンプルではない。ローカルコーパス
`~/.local/share/muse/sessions` では `"keep"` 3 件、`"kill"` 3 件。
**未知:** 他の enum 値、keep / kill の選択条件、headless 意味。

`muse exec --session-id <UUID>` は headless 用の **新規** run の id 指定。
**未知:** 既存 id を渡したときに `muse resume` 相当で履歴を継ぐかは未 probe。

### Headless と対話の差

| | 対話（`muse` / `muse resume`） | Headless（`muse exec`） |
| --- | --- | --- |
| UI | TUI。`--last` / uuid なしなら picker | TUI なし。1 prompt で終了 |
| 機械可読出力 | 未主張 | `--json` JSONL |
| 承認 | TUI で応答可 | TTY 承認 UI なし。フラグか待ち |
| 追加フラグ | （ルートオプションは適用） | `--max-model-steps`, `--prompt-file`, `--user-input-auto-resolve`, compaction しきい値, `--allow-workspace-switch` |
| セッションログ | 既定 ON | 既定 ON。`--no-session-log` は両方で利用可 |

---

## 2. 非対話での承認 / サンドボックス

既定は承認と sandbox **ON**（`muse --help` / `muse exec --help`）。

- `--approval-mode untrusted|on-request|never`（既定 **`on-request`**）
- `--approval-judge off|on`（既定 **on**）
- `--yolo` — 承認と sandbox を切り、workspace を trust（この run）
- `--disable-approval` — ツール承認プロンプトを無効（この run）
- `--disable-sandbox` / `--sandbox-network` / `--trust-workspace`

### ストール兆候（2026-09-06 再現）

コマンド（約 70 秒後に SIGTERM で kill）:

```sh
muse exec --provider meta --trust-workspace --max-model-steps 8 --json \
  --approval-mode untrusted --approval-judge off \
  "Run exactly this shell command and nothing else: echo 2350-stall-probe. ..."
```

- 約 70 秒生存し SIGTERM（`returncode 143`）。
- stderr: `workspace trust: trusted source=run-flag` のあと
  `received SIGTERM; flushed session logs`。
- `--json` stdout には待ち中の `approval_wait.*` が **出なかった**。
- `session.jsonl`（`01a07613-befd-7222-b513-29ac4d8a4f92`）には出た:
  - `runtime.session` `kind=approval` `event.kind=requested`
  - `approval_wait.effect.started`（`pending_action_id` あり）
  - SIGTERM 後: `event.kind=decision_applied` `decision=abort`
    `policy_result=deny`、`approval_wait.effect.terminal`
    `outcome.kind=cancelled`、`session.end` `exit_reason=clean`

過去ログ（`2026/09/04`–`09/05`）には terminal のない
`approval_wait.effect.started` も残っている。

**子プロセス 0 件:** `pgrep -P <muse-bin-exec-pid>` は承認待ちでも
モデル待ちでも空になり得る。`--approval-mode never` の実行中プロセス
（pid 73013、2026-09-06）もスナップショット時点で子 0。ストール判定は
**未完了の `approval_wait.effect.started` と組み合わせる**。単独の検出器ではない。

**未知:** 将来の CLI で `--json` が `approval_wait.*` を出すか。
1.0.3-R2198.1 の本 probe では session ログのみ。

### フラグによる回避

次のいずれか:

```sh
muse exec --yolo -- ...
muse exec --disable-approval -- ...
muse exec --approval-mode never -- ...
```

`--yolo` は sandbox も切り workspace を trust する。sandbox を残すなら
`--disable-approval` または `--approval-mode never`。

`--approval-mode on-request` と `--trust-workspace` では簡単な `echo`
はストールせず完了した（session `01a07613-1043-...`、
`run.terminal.completed` / `tool.result`、約 16 秒）。既定 `on-request` と
LLM approval judge は一部ツールを自動許可し得る。再現には
**`untrusted` + `--approval-judge off`** が確実だった。

実運用のオーケストレーション（ps、2026-09-06）:

```
muse-bin-1.0.3-R2198.1 exec --provider meta ... --trust-workspace \
  --approval-mode never --no-session-log --max-model-steps 120 ...
```

---

## 3. バックグラウンドタスクと成果物

観測範囲（製品仕様の完全な列挙ではない）:

- セッション内 **タスクストリーム**: `task.stream.linked`,
  `task.lifecycle.proposed|accepted|scheduled|side_effect_intent|started|status|completed`
  （`exec --json`）。
- セッションローカル **`cron.db`**: バイナリに `cron_create`（5 フィールド
  local cron、7 日 expiry）。`session.jsonl` と同じディレクトリ。
- `muse session-message` — クロスセッションメッセージ（未実行）。
- `session.started` の `background_tasks`: 観測値は `"keep"` と `"kill"`
  （ローカルコーパスで各 3 件）。`"keep"` は上記 resume サンプル由来ではない
  （`01a0692b-…` は `"kill"`）。**未知:** 他の enum 値、選択規則、headless 意味。
- `local-tracing/bootstrap/cli-*.log` — CLI 起動ごとの bootstrap。セッション
  transcript ではない。
- `tool-outputs/.spool/` — 途中 / 切り詰められたツール出力。
- `runtime/muse/ms-*.sock` — MSP session host socket と `.lease`。

読み取り専用。変更したのは `/tmp/muse-2350-*` の throwaway のみ。

---

## 4. Hook / イベント一覧

### 耐久ログ `session.jsonl` の payload 型（コーパス件数、ローカル store）

コマンド（調査時点のスナップショット。後続 probe で増え得る）:

```sh
grep -rho '"payload_type":"[^"]*"' ~/.local/share/muse/sessions | sort | uniq -c | sort -nr
```

上位に `runtime.session`、`tool_batch.effect.*`、
`session.resource_pressure.observed`、`session.opened.observed`、
`session.end`（59）、`session.resumed`（4）、`session.started`（2）、
`approval_wait.effect.started`（25）、`approval_wait.effect.terminal`（18）。
`hook` を含む `payload_type` は **0**（plugin 未導入:
`muse plugins list --json` → `{"plugins":[]}`）。

### ホストの hook 名（バイナリ `HookEventKind`、1.0.3-R2198.1）

`muse-bin-1.0.3-R2198.1` の strings から抽出（live dispatch ではない）:

`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`,
`PostToolUse`, `PreLLMCall`, `PostLLMCall`, `PreCompact`, `PostCompact`,
`SubagentStart`, `SubagentStop`, `Stop`, `SessionEnd`, `Notification`,
`PostToolUseFailure`, `StopFailure`, `PostToolBatch`。

同じバイナリのパッケージ信号:

- Manifest: root `plugin.json` に **Agent Plugins 1.0.0 `$schema`**、または
  `.muse-plugin` / `.codex-plugin` / `.claude-plugin` のいずれかちょうど 1 つの
  `plugin.json`。
- 慣例の `hooks/hooks.json`、env `MUSE_PLUGIN_ROOT`。
- `muse plugins validate <path> --json`（空 probe は上記メッセージで
  `missing-manifest`）。
- `muse plugins hook test <plugin-id>:<hook-id> --fixture <path>`。

**未知（明示）:**

- `$schema` の正確な URL（バイナリからクリーンな URL を回収できず。
  `https://json.schemastore.org/agent-plugins-1.0.0.json` は **未確認**）。
- 各 `HookEventKind` が `exec` / TUI / resume で実際に dispatch されるか
  （plugin 未導入。live probe は `v0.50.0-2`）。
- `exec` 終了時に plugin の `SessionEnd` が走るか（ログの `session.end`
  は hook 実行の証明ではない）。
- consolidation / Stop exit 2 相当: **unknown**。

### Traceary カバレッジ（本イシュー）

`application/hostcoverage/matrix.json` に `muse` host はなく、
`integrations/muse-plugin/` もない。Traceary 配線は **未実装**。
以下は調査予測であり matrix の編集ではない。

| Traceary イベント | Muse 側 signal | 今日の Traceary | 担当 |
| --- | --- | --- | --- |
| `session_started` | `SessionStart`（バイナリ） | unsupported / unwired | v0.50.0-2 plugin + v0.50.0-3 matrix |
| `prompt` | `UserPromptSubmit` | unsupported / unwired | 同上 |
| `command_executed` | `PostToolUse`, `PostToolUseFailure` | unsupported / unwired | 同上 |
| `transcript` | `Stop`（turn 境界かは **未検証**） | unsupported / unwired | 同上 |
| `compact_summary` | `PreCompact`, `PostCompact` | unsupported / unwired | 同上 |
| `session_ended` | `HookEventKind` の `SessionEnd`。ログの `session.end` は hook ではない | unsupported / unwired | -2 で live dispatch |
| `consolidation_request` | **unknown** | unsupported | -2 |

追加ホスト hook（Traceary 行なし）: `PermissionRequest`, `PreLLMCall`,
`PostLLMCall`, `SubagentStart`, `SubagentStop`, `Notification`,
`PostToolBatch`, `StopFailure`。live fixture のあと分類する。

将来の matrix セルの凡例: **wired** = パッケージ済み Traceary 捕捉;
**available** = ホスト hook はあるが Traceary は未購読; **unsupported**
= 使えるホスト signal がない。今日の Muse×lifecycle セルはパッケージが無いため
Traceary 側では実質すべて **unsupported**。

---

## 5. 兄弟先例（Muse plugin に必要なもの）

ここでは作らない。参照:

- パッケージ: `integrations/grok-plugin/` — `plugin.json`, `hooks/hooks.json`,
  `scripts/traceary-grok.sh`（薄い `traceary hook grok <action>`）、`skills/`
  （共有 skill 4 件）、`marketplace-entry.json`。
- Host matrix: `application/hostcoverage/matrix.go` + `matrix.json`。
  状態 enum: `wired` / `available` / `unsupported`。Muse 追加は新しい
  `hosts[]`（`id`, `package`, `doctor_client`, 各 `lifecycle_events` id の
  `events`）、その後 `go run ./cmd/repo-tooling docs generate-host-coverage`。
- Doctor client、`traceary hook <client>`、install script、二言語
  `docs/integrations/` ガイドは Grok/Kimi に倣う。

-2 向けの Muse 固有（本調査から）:

1. `muse plugins validate` が通る manifest（Agent Plugins 1.0.0
   `$schema` または `.muse-plugin/plugin.json`）。
2. Muse の `HookEventKind` 名を使う `hooks/hooks.json`。コマンドは
   `MUSE_PLUGIN_ROOT`。
3. Headless: `--disable-approval` / `--approval-mode never` / `--yolo`。
   ストールは `session.jsonl` の未完了 `approval_wait.effect.started`。
4. `--json` stdout を hook の代替にしない。
5. `wired` にする前に `exec` / TUI / `resume` を live 検証する。

---

## Headless オーケストレーション（要約）

ピン: **Muse Code 1.0.3 (1.0.3-R2198.1)** /
`/Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1`。

```sh
muse exec --provider meta --trust-workspace \
  --approval-mode never \
  --max-model-steps N \
  --json \
  --prompt-file path.md
```

ストール: プロセス生存、しばしば子 PID なし、
`session.jsonl` に `approval_wait.effect.started` と
`runtime.session` `kind=approval` `requested` があり
`approval_wait.effect.terminal` がない。
回避: `--approval-mode never` または `--disable-approval`（sandbox も切るなら
`--yolo`）。確認:

```sh
rg 'approval_wait' ~/.local/share/muse/sessions/YYYY/MM/DD/<id>/session.jsonl
pgrep -P <muse-bin-pid> || echo 'zero child processes'
```
