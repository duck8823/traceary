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

## headless print mode (`agy --print`) の capture level

headless な `agy --print` 実行で記録されるのは **session start + run_command audit のみ**です。`traceary doctor --client antigravity` はこれを `antigravity-capture-levels` チェックとして、以下の記録レベルのトークンで報告します:

| Antigravity event | 記録レベル | headless `agy --print` | interactive |
| --- | --- | --- | --- |
| `PreInvocation`（session start） | `start_supported` | ● 発火 — `conversationId` を key にした session 開始/更新 | ● 発火 |
| `PreToolUse` + `PostToolUse`（`run_command`） | `tool_audit_supported` | ● 発火 — 実行が `run_command` を使う場合に command audit | ● 発火 |
| `Stop`（transcript + turn 境界） | `final_turn_supported` / `final_turn_unavailable` | ✕ `final_turn_unavailable` — print mode では host が発行しない | ● `final_turn_supported` |

headless print mode では host が `Stop`（その他の finalization hook も含む）を
**発行しない**ため、その実行に対して Traceary は `transcript` event も turn 境界も
記録しません。これは 2026-06-20 の dogfooding（Traceary 0.21.3、`agy` 1.0.10）で
確認済みです: クリーンな `agy --print` smoke では `session_started`（`run_command`
が走れば `command_executed` も）は記録されましたが、`transcript` event は出ませんでした。
最終 transcript / turn 境界が記録されるのは host が `transcriptPath` 付き `Stop` を
発行したとき、すなわち interactive 実行に限られます。これは print mode の想定どおりの
記録レベルであり、Traceary の install 失敗ではありません: 4 つの hook が正しく
登録されていれば `doctor --client antigravity` は引き続き `pass` し、print mode で host
`Stop` が無いことは host 側のモード特性です。

> 記録された event の確認方法: hook 由来の Antigravity event は `client=hook`,
> `agent=antigravity` で保存されます。`traceary list --agent antigravity` で読み取って
> ください。`traceary list --client antigravity` ではこれらの event は 0 件になります
> （記録された client が文字どおり `antigravity` の event しか一致しないため）。`doctor` /
> `hooks install` の `--client antigravity` selector は別物で、どの host の checks/config を
> 実行するかを選ぶものです。

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

`doctor` は Antigravity の capability に加えて、**hook install 経路**ごとに 1 つずつ check を報告します。Antigravity は独立した 3 経路をサポートし、そのいずれか 1 つが有効であれば十分だからです。

- `antigravity-capability` — Antigravity のインストール（PATH 上の `agy`/`antigravity` CLI、またはアプリバンドル）を検出すると `pass`。Traceary は公開 hooks/plugin contract をサポートし、Traceary 側の認証は不要なためです。CLI もバンドルも無い場合は `not_installed`（warn）。この check はアプリを起動せず、ブラウザ自動操作も認証情報の読み取りも行いません。
- `antigravity-hooks-workspace` — workspace 経路（`<project>/.agents/hooks.json`）。
- `antigravity-hooks-user` — user-level 経路（`~/.gemini/config/hooks.json`）。
- `antigravity-cli-plugin` — `agy plugin install` が import する CLI plugin 経路のディレクトリ `~/.gemini/antigravity-cli/plugins/traceary` を検査します。サポートされた Antigravity の top-level hook-group 形式なら `pass`、**古い Gemini 形式のパッケージ**（legacy な top-level `{"hooks": ...}` 形式、または `traceary hook ... gemini` を呼び出す command）を見つけると `warn` を報告します。この check は `plugin.json`・`hooks.json`・`hooks/hooks.json` のみを読み取り、transcript や認証情報は読みません。
- `antigravity-hooks` — 集約サマリー。**いずれか**の経路の config が不正（経路別 check が `fail`）な場合は、別の経路が健全でも Antigravity が読み込めないため `fail` を報告します。それ以外では、**いずれか**の経路が健全なら `pass`、**どの**経路も健全でない場合のみ、導入手順を案内する actionable な `warn` を報告します。
- `antigravity-capture-levels` — 常に `pass`。hook が導入されているだけで transcript 完全記録を暗示しないよう、実行モードごとの **記録レベル** を報告する status 専用 check です。経路の導入健全性（上の `antigravity-hooks`）とは別物で、`start_supported`（PreInvocation）と `tool_audit_supported`（PreToolUse+PostToolUse `run_command`）はすべてのモードで、`final_turn_supported`（Stop → transcript + turn 境界）は対話実行でのみ記録されることを報告します。headless `agy --print` では host が print mode で `Stop`/finalization hook を発行しないため `final_turn_unavailable` となります。これは print mode における想定どおりの記録レベルであり導入失敗ではないため、check は `pass` のままです。

**各経路は単体では任意です。** 経路が無い場合は `warn` ではなく `skip` を報告します。たとえば user-level または CLI plugin の経路が健全であれば、存在しない workspace `.agents/hooks.json` は `skip` 扱いとなり、`antigravity-hooks` サマリーは `pass` のままです。doctor が hook coverage について warn するのは、3 経路のいずれも `traceary` グループを登録していないときだけです。経路ファイルが存在するが不正（JSON オブジェクトでない）な場合は、他経路の状態に関わらず Antigravity 自体が読み込めないため `fail` を報告します。

Antigravity はデフォルトの doctor client 一覧（`["claude","codex","gemini"]`）に含まれません。明示的に `--client antigravity` を指定してください。

## 古い Gemini import の plugin を移行する

以前に Gemini CLI 経由で Traceary plugin を import していた場合、`~/.gemini/antigravity-cli/plugins/traceary` に **古い Gemini 形式**（command が `traceary hook ... gemini` を呼ぶ top-level `{"hooks": ...}` 形式）が残っていることがあります。この状態では `agy plugin install` がパッケージを置き換えずに success を報告することがあり、Antigravity セッションが Antigravity ではなく Gemini の hook runtime に配線されたままになります。サポートされるパッケージは `traceary hook antigravity ...` を呼ぶ `traceary` グループを持つ top-level hook-group 形式です。

`traceary doctor --client antigravity` はこれを `antigravity-cli-plugin` の warning として表示します。修復するには、古いディレクトリを削除してサポートされるパッケージを再インストールしてください。

```sh
rm -rf ~/.gemini/antigravity-cli/plugins/traceary
agy plugin install integrations/antigravity-plugin
# または CLI plugin を使わず hook を直接配線する:
traceary hooks install --client antigravity --upgrade
```

`traceary doctor --client antigravity` を再実行し、check が `pass` に変わることを確認してください。

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
