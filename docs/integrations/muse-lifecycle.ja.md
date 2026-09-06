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

```sh
muse --version
ls -la "$(command -v muse)"
ls -la /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
file /Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1
```

---

## 1. セッションライフサイクル

### 開始

- 対話: `muse [OPTIONS] [PROMPT]` は TUI（`muse --help`: サブコマンドなしなら
  interactive TUI）。
- Headless: `muse exec [OPTIONS] [PROMPT]` は **プロンプト 1 回** で終了
  （`muse exec --help`）。
- `muse exec --json` は stdout に MSP JSONL。echo provider の観測
  （`--provider echo --no-session-log`）:
  `runtime.command.accepted`, `session.run.linked`,
  `session.workspace_branch.observed`, `turn.input.user`,
  `run.lifecycle.started`, `task.stream.linked`, `task.lifecycle.*`,
  `run.output.delta`, `run.terminal.completed`。終了コード `0`。
- `--no-session-log` を付けない場合、耐久ログに
  `session.opened.observed`（`resume: false`）、
  `session.startup_phases.observed`、後に `session.end` が残る。
  例: `.../2026/09/03/01a067b6-81b6-7490-91b0-494857e673a1/session.jsonl`
  - `session.end` → `exit_reason: "clean"`

`--no-session-log` はディスクへ書かず、session messaging も無効:

```
muse: local session messaging disabled: session logging is required
```

### 永続化

```
~/.local/share/muse/sessions/YYYY/MM/DD/<session-id>/
  session.jsonl
  cli-*.log
  cron.db[+shm|+wal]
  session.peer-history.sqlite3
  tool-outputs/.spool/
  approval-review/
  .session.lock
```

索引: `~/.local/share/muse/session-index.db` の `sessions` テーブル。

完了した `muse exec`（session `01a07613-1043-75a2-b471-661929150803`）は
`sessions/2026/09/06/<id>/session.jsonl`（95 行）と
`sessions/.msp-view-v1/<id>` を書いた。

### 再開

```
muse resume
muse resume --last
muse resume <uuid>
```

再開は **対話 TUI** であり headless 一発実行ではない。耐久ログに
`session.resumed`（例: `prior_turn_count: 3`）。`session.started` には
`"background_tasks": "keep"` も観測。

`muse exec --session-id <UUID>` は headless 用の id 指定。
**未知:** 既存 id を渡したときに `muse resume` 相当で履歴を継ぐかは未 probe。

### Headless と対話の差

| | 対話（`muse` / `resume`） | Headless（`exec`） |
| --- | --- | --- |
| UI | TUI / picker | TUI なし。1 prompt で終了 |
| 機械可読出力 | 未主張 | `--json` JSONL |
| 承認 | TUI で応答可 | TTY 承認 UI なし。フラグか待ち |
| 追加フラグ | ルートオプション | `--max-model-steps` 等 |

---

## 2. 非対話での承認 / サンドボックス

既定は承認と sandbox **ON**（`muse exec --help`）。

- `--approval-mode untrusted|on-request|never`（既定 **`on-request`**）
- `--yolo` — 承認と sandbox を切り、workspace を trust（この run）
- `--disable-approval` — ツール承認プロンプトを無効（この run）

### ストール兆候（2026-09-06 再現）

```sh
muse exec --provider meta --trust-workspace --max-model-steps 8 --json \
  --approval-mode untrusted --approval-judge off \
  "Run exactly this shell command and nothing else: echo 2350-stall-probe. ..."
```

- 約 70 秒生存し SIGTERM（`returncode 143`）。
- stderr: `received SIGTERM; flushed session logs`
- `--json` stdout には待ち中の `approval_wait.*` が **出なかった**。
- `session.jsonl`（`01a07613-befd-7222-b513-29ac4d8a4f92`）には出た:
  - `runtime.session` `kind=approval` `requested`
  - `approval_wait.effect.started`
  - SIGTERM 後: `decision_applied` `abort` / `approval_wait.effect.terminal`
    `cancelled` / `session.end`

過去ログ（`2026/09/04`–`09/05`）には terminal のない
`approval_wait.effect.started` も残っている。

**子プロセス 0 件:** `pgrep -P <muse-bin-exec-pid>` は承認待ちでも
モデル待ちでも空になり得る。`--approval-mode never` の実行中プロセス
（pid 73013）もスナップショット時点で子 0。ストール判定は
**未完了の `approval_wait.effect.started` と組み合わせる**。

**未知:** 将来の CLI で `--json` が `approval_wait.*` を出すか。
1.0.3-R2198.1 の本 probe では session ログのみ。

### フラグによる回避

```sh
muse exec --yolo -- ...
muse exec --disable-approval -- ...
muse exec --approval-mode never -- ...
```

sandbox を残すなら `--yolo` より `--disable-approval` または
`--approval-mode never`。

`--approval-mode on-request` と `--trust-workspace` では簡単な `echo`
はストールせず完了した（session `01a07613-1043-...`）。再現には
**`untrusted` + `--approval-judge off`** が確実だった。

---

## 3. バックグラウンドタスクと成果物

観測範囲（製品仕様の完全な列挙ではない）:

- タスクストリーム: `task.lifecycle.*`（`exec --json`）
- セッションローカル `cron.db`（バイナリに `cron_create` 文字列）
- `muse session-message`（未実行）
- `session.started` の `background_tasks: "keep"`。**未知:** 他の値と
  headless 意味
- `local-tracing/bootstrap/cli-*.log`
- `tool-outputs/.spool/`
- `runtime/muse/ms-*.sock`

読み取り専用。変更したのは `/tmp/muse-2350-*` の throwaway のみ。

---

## 4. Hook / イベント一覧

### 耐久ログ

ローカル store の `payload_type` 上位に `runtime.session`、
`approval_wait.effect.*`、`session.end`、`session.resumed` など。
`hook` を含む `payload_type` は **0**（`muse plugins list --json` は
`{"plugins":[]}`）。

### ホストの hook 名（バイナリ `HookEventKind`）

`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`,
`PostToolUse`, `PreLLMCall`, `PostLLMCall`, `PreCompact`, `PostCompact`,
`SubagentStart`, `SubagentStop`, `Stop`, `SessionEnd`, `Notification`,
`PostToolUseFailure`, `StopFailure`, `PostToolBatch`。

パッケージ: Agent Plugins 1.0.0 の `$schema` 付き root `plugin.json`、
または `.muse-plugin` / `.codex-plugin` / `.claude-plugin` のいずれか
1 つ。`hooks/hooks.json` と `MUSE_PLUGIN_ROOT`。
`muse plugins validate` は不正 manifest で `missing-manifest`。

**未知:**

- `$schema` の正確な URL（バイナリからクリーンな URL を回収できず）
- 各 `HookEventKind` が `exec` / TUI / resume で実際に dispatch されるか
  （plugin 未導入。live probe は -2）
- `exec` 終了時に plugin の `SessionEnd` が走るか（ログの `session.end`
  は hook 実行の証明ではない）
- consolidation / Stop exit 2 相当: **unknown**

### Traceary カバレッジ（本イシュー）

`matrix.json` に `muse` host はなく、`integrations/muse-plugin/` もない。
以下は調査予測であり matrix の編集ではない。

| Traceary イベント | Muse 側 signal | 今日の Traceary | 担当 |
| --- | --- | --- | --- |
| `session_started` | `SessionStart` | unsupported / unwired | v0.50.0-2 / -3 |
| `prompt` | `UserPromptSubmit` | unsupported / unwired | 同上 |
| `command_executed` | `PostToolUse`, `PostToolUseFailure` | unsupported / unwired | 同上 |
| `transcript` | `Stop`（turn 境界かは未検証） | unsupported / unwired | 同上 |
| `compact_summary` | `PreCompact`, `PostCompact` | unsupported / unwired | 同上 |
| `session_ended` | `HookEventKind` の `SessionEnd`。ログの `session.end` は hook ではない | unsupported / unwired | -2 で live dispatch |
| `consolidation_request` | **unknown** | unsupported | -2 |

追加ホスト hook（Traceary 行なし）: `PermissionRequest`, `PreLLMCall`,
`PostLLMCall`, `SubagentStart`, `SubagentStop`, `Notification`,
`PostToolBatch`, `StopFailure`。

今日の Traceary 側はパッケージが無いため、実質すべて **unsupported**。

---

## 5. 兄弟先例（Muse plugin に必要なもの）

ここでは作らない。参照:

- `integrations/grok-plugin/` — `plugin.json`, `hooks/hooks.json`,
  `scripts/traceary-grok.sh`, 共有 skill 4 件, `marketplace-entry.json`
- `application/hostcoverage/matrix.go` / `matrix.json` —
  `wired` / `available` / `unsupported`。Muse 追加は `hosts[]` と
  `docs generate-host-coverage`

-2 向けの Muse 固有:

1. `muse plugins validate` が通る manifest
2. Muse の `HookEventKind` と `MUSE_PLUGIN_ROOT`
3. Headless は `--disable-approval` / `--approval-mode never` / `--yolo`。
   ストールは `session.jsonl` の未完了 `approval_wait.effect.started`
4. `--json` stdout を hook の代替にしない
5. `wired` にする前に `exec` / TUI / `resume` を live 検証する

---

## Headless オーケストレーション（要約）

ピン: **Muse Code 1.0.3 (1.0.3-R2198.1)** /
`/Users/duck8823/.local/bin/muse-bin-1.0.3-R2198.1`

```sh
muse exec --provider meta --trust-workspace \
  --approval-mode never \
  --max-model-steps N \
  --json \
  --prompt-file path.md
```

ストール: プロセス生存、しばしば子 PID なし、
`session.jsonl` に `approval_wait.effect.started` と
`kind=approval` `requested` があり `approval_wait.effect.terminal` がない。
回避: `--approval-mode never` または `--disable-approval`（sandbox も切るなら
`--yolo`）。
