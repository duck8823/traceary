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
| `Stop` | `transcriptPath` から最新の user prompt と model response を復元し、`prompt` + `transcript`、続いて turn 境界を記録。session は**閉じない** |

Antigravity の payload は camelCase フィールド（`conversationId`, `workspacePaths`, `transcriptPath`, `toolCall.name`, `toolCall.args.CommandLine`, `toolCall.args.Cwd`, `stepIdx`, `terminationReason`）を使います。Traceary は共有 runtime（session / audit / transcript）を再利用する前に、これらを内部形式へ正規化します。

packaged plugin は `mcp_config.json` を通じてローカルの `traceary mcp-server` も公開し、`traceary-session-history`、`traceary-memory-review`、`traceary-memory-remember` の文脈 skill を同梱します。`traceary hooks install` の直接設定は hook のみを導入します。Antigravity に MCP tool と skill を自動検出させる場合は packaged plugin を使用してください。

## status line からの usage metadata

Antigravity の hook payload は provider usage を公開しません。一方、独立した status-line payload には本文を含まない累積合計があります。Traceary は次の内部合成コマンドでこの payload を受け取れます。

```sh
traceary hook antigravity statusline
```

このコマンドだけを status-line renderer に設定しないでください。表示用の出力を意図的に生成しないためです。Bash では、たとえば既存 renderer と次のように合成します。

```bash
#!/usr/bin/env bash
tee >(traceary hook antigravity statusline >/dev/null 2>&1) |
  "$HOME/.local/bin/my-antigravity-statusline"
```

この wrapper を `~/.gemini/antigravity-cli/settings.json` で指定します。

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.gemini/antigravity-cli/traceary-statusline.sh"
  }
}
```

この設定は任意であり、`traceary hooks install` は変更しません。

Traceary は `idle` payload だけを受け入れ、`total_input_tokens` と `total_output_tokens` を conversation / model source の最新 immutable snapshot として保存します。古い snapshot は加算せず、後続 snapshot で置き換えます。`current_usage`、cache counter、quota、email、account field、cost は無視します。同じ snapshot の再配信は冪等で、累積値の減少は安全側に倒して拒否します。`Stop` transcript から安定した完了 turn step を取得できた場合、その provider call と対応付けられない境界を usage **unavailable** として別に保存します。安定した step が無い場合は call identity を作りません。transcript の長さや command 回数から usage を推測することもありません。

## 制限事項

- **`SessionStart` が無い。** conversation 単位で最初に発火するのは `PreInvocation`（毎回のモデル呼び出し前に発火）なので、Traceary はこれを `conversationId` を key にした冪等な session 開始/更新として使います。
- **`Stop` は execution 単位の境界であり session 終了ではない**（Codex と同じモデル — #1170）。session 行は開いたままで（memory auto-extract は発火）、MCP `manage_session` または stale GC（`traceary session gc`）でのみ終了します。
- **audit 対象は `run_command` tool 呼び出しのみ。** `PostToolUse` は `stepIdx`/`error` のみを持ち command args を持たないため、args を持つ `PreToolUse` と step 単位で突き合わせます。`run_command` 以外の tool は何も記録しません。
- **prompt 本文は直接の hook field ではありません。** 公開 hook payload は `transcriptPath` を提供します。Traceary は Stop 時に最新の `USER_INPUT` / `USER_EXPLICIT` 行を復元します。
- **transcript 抽出は best effort。** 文書化された `transcriptPath` のファイルは `transcript.jsonl` です。Traceary は現在の CLI の `MODEL` / `*_RESPONSE` 行と従来の nested/flat 形式を処理し、thinking/text を分離して保持します。未知の形式は黙ってスキップします。
- **認証情報・keychain・cookie・ブラウザストレージは一切読みません。** ディスクから読むのは文書化された `transcriptPath` hook フィールドのみです。
- **status-line usage は一部対応かつ任意設定です。** conversation / model 単位の累積合計であり、provider call identity ではありません。Traceary は status snapshot と `Stop` を対応付けません。

## headless print mode (`agy --print`) の capture level

現在の Antigravity hooks は headless と interactive で同じ lifecycle signal を提供します。

| Antigravity event | 記録レベル | headless `agy --print` | interactive |
| --- | --- | --- | --- |
| `PreInvocation`（session start） | `start_supported` | ● `conversationId` 単位の開始/更新 | ● |
| `PreToolUse` + `PostToolUse`（`run_command`） | `tool_audit_supported` | ● `run_command` 使用時 | ● |
| `Stop`（`prompt` + `transcript` + turn 境界） | `final_turn_supported` | ● 現行 CLI は `transcriptPath` 付き Stop を発行 | ● |

2026-07-13 に `agy` 1.1.1 と現行の公式 hook contract で再確認しました。公開 payload は prompt 本文を直接持ちませんが、すべての hook が `transcriptPath` を受け取ります。Traceary は Stop 時にそのファイルから最新の明示 user input と model response を読み取ります。hook 設定が健全でも file の読み取りや event 永続化までは証明できないため、`antigravity-event-coverage` が recent DB 証拠を検査し、transcript coverage が設定 threshold を下回ると警告します。

`workspacePaths` が空のとき（`agy` 1.1.x の untrusted や一部 headless 実行で観測）は、hook プロセスの cwd（`hooks.json` があるディレクトリ）ではなく、Antigravity host プロセスの cwd 連鎖から project workspace を復元します。この fallback がないと、hook 自体は成功していても empty workspace で保存され、既定の `traceary list` workspace filter では見えません。

> 記録された event の確認方法: hook 由来の Antigravity event は `client=hook`,
> `agent=antigravity` で保存されます。`traceary list --agent antigravity` で読み取って
> ください。`traceary list --client antigravity` ではこれらの event は 0 件になります。

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

代わりに、同梱の plugin（[`integrations/antigravity-plugin/`](../../integrations/antigravity-plugin/)）を導入できます。同じ `traceary` hook グループに加え、公式 Antigravity スキーマに従う version 付き `plugin.json`、Traceary MCP server 用の `mcp_config.json`、共有の memory/session skill 3 件を同梱しています。

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
- `antigravity-mcp` — 導入済み CLI plugin の `mcp_config.json` に `traceary mcp-server` 登録があれば `pass` します。plugin はあるが登録がなければ `warn`、plugin 経路自体がなければ `skip` します。hook の直接設定は MCP tool を提供しないためです。
- `antigravity-hooks` — 集約サマリー。**いずれか**の経路の config が不正（経路別 check が `fail`）な場合は、別の経路が健全でも Antigravity が読み込めないため `fail` を報告します。それ以外では、**いずれか**の経路が健全なら `pass`、**どの**経路も健全でない場合のみ、導入手順を案内する actionable な `warn` を報告します。
- `antigravity-capture-levels` — 常に `pass`。公開 hook の設定上の capability として、interactive と現行 headless CLI の `start_supported`、`tool_audit_supported`、`final_turn_supported` を報告します。
- `antigravity-event-coverage` — recent な `agent=antigravity` の DB 証拠を検査します。hook 導入経路がすべて健全でも、十分な sample の開始済み session に transcript event が無ければ警告します。
- `antigravity-plugin-version` — 導入済み plugin manifest と実行中 Traceary release の version を比較し、不一致なら `warn` を報告します。Traceary 更新後は packaged plugin も再導入してください。

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

Antigravity validator では skill が `3 processed`、MCP server と hook group がそれぞれ `1 processed` と表示されます。

## 公式リファレンス

2026-06-20 JST に確認:

- Antigravity 2.0 hooks: https://antigravity.google/assets/docs/antigravity-2-0/hooks.md
- Antigravity IDE hooks: https://antigravity.google/assets/docs/editor/ide-hooks.md
- Antigravity CLI plugins: https://antigravity.google/assets/docs/cli/cli-plugins.md
- Antigravity 2.0 plugins: https://antigravity.google/assets/docs/antigravity-2-0/plugins.md
- Antigravity IDE plugins: https://antigravity.google/assets/docs/editor/ide-plugins.md
- Antigravity CLI install: https://antigravity.google/assets/docs/cli/cli-install.md

Gemini CLI から移行する場合、既存の Gemini CLI インストールでは引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用できます。
