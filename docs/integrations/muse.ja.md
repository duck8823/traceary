# Muse Code プラグイン

[English](./muse.md)

Traceary v0.50.0 で native な Muse Code 連携を追加しました。
[`integrations/muse-plugin/`](../../integrations/muse-plugin/) のパッケージは
lifecycle hook 8 件、ローカル Traceary CLI、共有 skill 4 件（[skills](./skills.ja.md)）
を宣言します。記録される hook event は `client=hook` / `agent=muse` です。
host pin は **Muse Code 1.0.3** です。live の session corpus はまだ無いため、
doctor の session enrichment はオフです。

## インストール

この slice に `scripts/install-muse-plugin.sh` はありません。packaged directory を
Muse の plugin flow で導入し、次で確認します。

```sh
muse plugins validate integrations/muse-plugin --json
muse plugins list --json
traceary doctor --client muse --json
```

`muse plugins list --json` は plugin 未導入時に `{"plugins":[]}` と観測されています
（#2350 §4）。非空の list 形は merge gate ではありません。doctor は未知 JSON を
FAIL ではなく WARN にします。

## Headless 実行

既定の Muse は承認と sandbox が **ON** です。無人の `muse exec` では次のいずれかを使います。

```sh
muse exec --disable-approval -- ...
muse exec --approval-mode never -- ...
muse exec --yolo -- ...
```

stall の署名: `session.jsonl` に `approval_wait.effect.started` があり、対応する
`approval_wait.effect.terminal` が無い状態（#2350 §2）。`--json` stdout を hook の
代替にしないでください。

## サポートするカバレッジ

packaged hook 8 件（#2352 runtime、`integrations/muse-plugin/hooks/hooks.json`）:

| Muse event | Traceary action | Matrix cell |
| --- | --- | --- |
| `SessionStart` | `traceary hook muse session-start` | `session_started` **wired** |
| `UserPromptSubmit` | `traceary hook muse user-prompt-submit` | `prompt` **wired** |
| `PreToolUse` | `traceary hook muse pre-tool-use` | 検証のみ |
| `PostToolUse` | `traceary hook muse post-tool-use` | `command_executed` **available**（宣言済み。live の tool-dispatch capture probe なし） |
| `PostToolUseFailure` | `traceary hook muse post-tool-use-failure` | `command_executed` **available**（同上） |
| `Stop` | `traceary hook muse stop` | `transcript` **wired**（turn 境界であり session 終了ではない） |
| `PreCompact` | `traceary hook muse pre-compact` | `compact_summary` **available**（宣言済み。exec/TUI/resume での dispatch は未観測） |
| `PostCompact` | `traceary hook muse post-compact` | `compact_summary` **available**（同上） |

matrix の Muse cell からコピーした正直な caveat:

- **`session_ended` available** — binary の `HookEventKind` に `SessionEnd` はあるが、
  packaged `hooks.json` は購読しない。durable な `session.end` は log event であり
  hook 証明ではない（#2350 findings §4）。
- **`consolidation_request` unsupported** — 使える host 信号なし。Stop-exit-2 相当は
  明示的に unknown（#2350 §4）。
- **`command_executed` / `compact_summary` available** — `hooks.json` で宣言済み。
  live dispatch を観測するまで wired の capture 主張ではない。

[host coverage matrix](../hooks/host-coverage.ja.md) を参照してください。確認は
`traceary doctor --client muse`（`muse-cli` / `muse-plugin` / `muse-hooks` /
`muse-skills`）。SessionEnd 未購読かつ compact/tool dispatch 未観測の間、
`muse-hooks` は WARN のままです。

## Spool replay

遅延した Muse hook は command `muse` で persist します。doctor の spool drain は
上記 8 action を `replayMuseSpoolRecord` 経由で再実行します（Kimi/Grok と同じ形）。
replay case が無いと `unsupported hook spool command: muse` で失敗します。
