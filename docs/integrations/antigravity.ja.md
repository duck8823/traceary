# Antigravity hooks / plugin

[English](./antigravity.md)

Antigravity は、Google の AI エージェントホストとして Gemini CLI の後継です。**v0.21.1** 以降、Traceary は Antigravity を実際の hook client としてサポートし、packaged plugin を提供します。利用するのは公開されている hook/plugin サーフェスのみで、認証情報の読み取り・アプリ内部の非公開フォーマット・ブラウザ自動操作は一切行いません。

> **v0.21.0 からの変更点:** v0.21.0 では Antigravity の capability **診断のみ**（`doctor --client antigravity` → `tool_unavailable`）を提供し、当時は公開 contract が確認できなかったため hook / package を意図的に提供しませんでした。その後 Google が公開 Antigravity hook/plugin/CLI サーフェスを公開したため、v0.21.1 では Antigravity を診断専用ホストからサポート対象の hook client へと切り替えます。

## 自動配線される内容

Antigravity の `hooks.json` は *hook-group 名* から event config への top-level map（Traceary は `traceary` グループを所有）で、他ホストが共有する `{"hooks": {...}}` 形式とは異なります。Traceary はこのホスト向けに独自の document を render / merge します。

| Antigravity event | Traceary の効果 |
| --- | --- |
| `PreInvocation` | `conversationId` を key にした冪等な session 開始/更新（Antigravity に `SessionStart` はありません）。最初の workspace path を workspace とする |
| `PreToolUse` (`run_command`) | 提案された `{CommandLine, Cwd}` を `conversationId + stepIdx` で永続化。ブロックしない（`{"decision":"allow"}`） |
| `PostToolUse` (`run_command`) | 同一 step の `PreToolUse` が永続化した command を突き合わせ、`command_executed` audit を記録（step の `error` 付き）。pending が無ければ fail soft |
| `Stop` | `transcriptPath` から turn の transcript（best effort）と turn 境界を記録。session は**閉じない** |

Antigravity の payload は camelCase フィールド（`conversationId`, `workspacePaths`, `transcriptPath`, `toolCall.name`, `toolCall.args.CommandLine`, `toolCall.args.Cwd`, `stepIdx`, `terminationReason`）を使います。Traceary は共有 runtime（session / audit / transcript）を再利用する前に、これらを内部形式へ正規化します。

## 制限事項

- **`SessionStart` が無い。** conversation 単位で最初に発火するのは `PreInvocation`（毎回のモデル呼び出し前に発火）なので、Traceary はこれを `conversationId` を key にした冪等な session 開始/更新として使います。
- **`Stop` は execution 単位の境界であり session 終了ではない**（Codex と同じモデル — #1170）。session 行は開いたままで（memory auto-extract は発火）、MCP `manage_session` または stale GC（`traceary session gc`）でのみ終了します。
- **audit 対象は `run_command` tool 呼び出しのみ。** `PostToolUse` は `stepIdx`/`error` のみを持ち command args を持たないため、args を持つ `PreToolUse` と step 単位で突き合わせます。`run_command` 以外の tool は何も記録しません。
- **transcript 抽出は best effort。** 文書化された `transcriptPath` のファイルは `transcript.jsonl` ですが、その行ごとのスキーマは公開 hook contract の一部ではないため、抽出器は複数の妥当な JSONL 形状から assistant の text/thinking ブロックを寛容に走査し、それ以外は黙ってスキップします。
- **認証情報・keychain・cookie・ブラウザストレージは一切読みません。** ディスクから読むのは文書化された `transcriptPath` hook フィールドのみです。

## インストール

1. まず Traceary CLI をインストールします。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Antigravity 向けの Traceary hook をインストールします。

```sh
# workspace レベル → <project>/.agents/hooks.json
traceary hooks install --client antigravity --project-dir .

# または user レベル → ~/.gemini/config/hooks.json
traceary hooks install --client antigravity --global
```

alias `agy` と `antigravity-cli` は canonical な `antigravity` client に解決されます。インストールは非破壊で、置換されるのは `traceary` hook グループのみ、その他の top-level hook グループはそのまま保持されます。`--upgrade` で再実行すると、ユーザー追加グループを保持したまま managed グループを更新します。

代わりに、同梱の plugin（[`integrations/antigravity-plugin/`](../../integrations/antigravity-plugin/)）を追加することもできます。これは同じ `traceary` hook グループを `hooks.json` として、公式 Antigravity plugin スキーマに従う `plugin.json` manifest とともに同梱しています。

## セットアップガイド

```sh
traceary hooks guide --client antigravity --project-dir .
```

install command、doctor command、想定 config path、および Antigravity 固有の注記（PreInvocation の session モデル、Stop の turn 境界、run_command の突き合わせ）を表示します。

## Doctor

```sh
traceary doctor --client antigravity --json
```

`doctor` は Antigravity 向けに 2 つの check を報告します。

- `antigravity-capability` — Antigravity のインストール（PATH 上の `agy`/`antigravity` CLI、またはアプリバンドル）を検出すると `pass`。Traceary は公開 hooks/plugin contract をサポートし、Traceary 側の認証は不要なためです。CLI もバンドルも無い場合は `not_installed`（warn）。この check はアプリを起動せず、ブラウザ自動操作も認証情報の読み取りも行いません。
- `antigravity-config` — 解決された `hooks.json` に `traceary` hook グループが登録されているかを報告し、グループが無い場合は `--upgrade` での修復を案内します。

Antigravity はデフォルトの doctor client 一覧（`["claude","codex","gemini"]`）に含まれません。明示的に `--client antigravity` を指定してください。

## ローカルでの調査結果

ローカル開発環境で観測した内容です。

| プロパティ | 値 |
| --- | --- |
| アプリパス | `/Applications/Antigravity.app` |
| Bundle ID | `com.google.antigravity` |
| URL スキーム | `antigravity://` |
| workspace hooks パス | `<project>/.agents/hooks.json` |
| global hooks パス | `~/.gemini/config/hooks.json` |

## パッケージ検証

```sh
agy plugin validate integrations/antigravity-plugin
# リポジトリ内の構造検証:
go run ./cmd/repo-tooling integrations verify
```

## 公式リファレンス

2026-06-20 JST に確認:

- Antigravity 2.0 hooks: https://antigravity.google/assets/docs/antigravity-2-0/hooks.md
- Antigravity IDE hooks: https://antigravity.google/assets/docs/editor/ide-hooks.md
- Antigravity CLI plugins: https://antigravity.google/assets/docs/cli/cli-plugins.md
- Antigravity 2.0 plugins: https://antigravity.google/assets/docs/antigravity-2-0/plugins.md
- Antigravity IDE plugins: https://antigravity.google/assets/docs/editor/ide-plugins.md
- Antigravity CLI install: https://antigravity.google/assets/docs/cli/cli-install.md

Gemini CLI から移行する場合、既存の Gemini CLI インストールでは引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用できます。
