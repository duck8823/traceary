# Grok Build plugin

[English](./grok-plugin.md)

Traceary v0.23.0 は Grok Build をネイティブにサポートします。
[`integrations/grok-plugin/`](../../integrations/grok-plugin/) のパッケージは、
検証済みのライフサイクル hook 7件、ローカルの Traceary MCP server 1件、
共通のメモリ・セッション skill 3件を導入します。hook 由来のイベントは
`client=hook`、`agent=grok` として記録します。

## 対応範囲

ライフサイクル用パッケージは、Grok Build 0.2.99 で実際に確認した hook 契約を
対象にします。usage 取得は、Grok Build 0.2.106 の headless 終端契約にも固定します。

| Grok event | Traceary の処理 |
| --- | --- |
| `SessionStart` | Grok セッションを開始または更新する |
| `UserPromptSubmit` | ユーザープロンプトを記録する |
| `PreToolUse` | 完了済み監査を書き込まず、tool payload を検証する |
| `PostToolUse` | 完了したコマンド監査を1件記録する。確認済みのファイル不在・拒否結果も失敗として扱う |
| `Stop` | `updates.jsonl` から可能な範囲で transcript を記録し、turn 境界を作る。セッションは終了しない |
| `PreCompact` / `PostCompact` | compact 前後の marker を別々に記録する。Grok は summary 本文を公開しない |

## usage の利用可否

Grok の native hook は provider usage を公開しません。そのため Traceary は、
検証済みの `promptId` で安定した境界を識別できる `Stop` に限り、集計対象外の
`unavailable` call observation を記録します。transcript、compact marker、retry、
subagent、response 本文から token 数を推定しません。安定した識別子がない Stop では、
usage 行を合成しません。

終了範囲が明確な headless 実行では、Grok の検証済み終端 stream を Traceary 管理の
完結型ライフサイクルから利用します。

```sh
traceary session run -- \
  grok --no-auto-update -p "your prompt" --output-format streaming-json
```

Traceary は stdout を変更せず転送し、終端 `end` の metadata だけを読み取ります。
`requestId`/`sessionId` ごとに `end.usage` を1件保存し、input、cache-read input、
output、reasoning、total token を記録します。途中の thought/text event、cost field、
error 本文、transcript 本文は破棄します。終端 usage object がない場合はゼロではなく、
同じ portable provider identity を維持したまま、集計対象外の `unavailable` run
observation を1件記録します。不正・競合・上限超過の終端 metadata は fail closed し、
代替 observation を生成しません。`modelUsage` がモデルを1件だけ示す場合はそのモデル名を
残します。複数モデルにまたがる合計はモデル未特定の
まま保存し、分割や二重集計をしません。
provider の `requestId` / `sessionId` の組は、長さ固定の portable identity に正規化します。
そのため、同じ終端結果が別の Traceary wrapper session から再送されても冪等です。
同じ identity で counter が変化した場合は、安全側に倒して競合として拒否します。

retry と subagent の利用量は、Grok の終端合計に含まれる範囲だけを採用します。
Traceary は途中 event を加算せず、回数も推定しません。compact hook の count は
provider usage ではなく context 圧縮の測定値なので、ライフサイクル記録だけに使います。
TUI の usage 経路は対応済みと表明しません。

`SessionEnd`、独立した失敗 hook、subagent の親子関係は payload を実環境で
確認できていないため、v0.23.0 の対応対象に含めません。Traceary は利用できない
ライフサイクル関係を推測で生成しません。field 単位の状態は
[host coverage matrix](../hooks/host-coverage.ja.md) と
[Grok の機械可読契約](../hooks/host-contract.json)を参照してください。

## インストール

1. Traceary CLI を導入し、`traceary` が `PATH` 上にあることを確認します。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### A. Public marketplace（掲載後の推奨）

[xAI Plugin Marketplace](https://github.com/xai-org/plugin-marketplace) に
Traceary が載っている場合、本リポジトリを clone せず Grok Build から導入できます。

```sh
# Grok の plugin UI / marketplace install 後:
traceary doctor --client grok --project-dir . --json
```

カタログ投稿用メタデータ:

- テンプレート: [`integrations/grok-plugin/marketplace-entry.json`](../../integrations/grok-plugin/marketplace-entry.json)
- commit pin: `./scripts/generate-grok-marketplace-entry.sh [git-ref]`
- 手順: [Grok marketplace 投稿](./grok-marketplace-submission.ja.md)

### B. ローカル導入（決定的 fallback）

matching release tag から常に利用できます。installer は検証・置換・inventory 表示を行います。

```sh
git clone --branch v0.27.0 --depth 1 https://github.com/duck8823/traceary.git
cd traceary
./scripts/install-grok-plugin.sh
```

installer は `grok plugin install --trust` を実行します。信頼した command hook は
ローカルで実行されるため、実行前にパッケージを確認してください。現行パッケージは
文書化された Traceary hook entrypoint のみを呼び出し、Grok の認証情報やブラウザ状態を
読み取ったり送信したりしません。

### clean-home 検証

```sh
./scripts/verify-grok-plugin-clean-home.sh
```

一時 `HOME` で validate → install → details → reinstall → uninstall を行い、
operator の credentials / browser state には触れません。

### Doctor

```sh
traceary doctor --client grok --project-dir . --json
```

正常な導入では `grok-cli`、`grok-plugin`、`grok-hook-trust`、`grok-hooks`、
`grok-mcp`、`grok-skills` が `pass` になります。`grok-event-coverage` は直近 DB
証拠を評価し、セッションが3件未満なら誤 pass せず「まだ判定しない」と報告します。

## プロジェクト hook と trust

hook、MCP、skill をまとめて配線できるため、ネイティブ plugin を推奨します。
hook だけを導入することもできます。

```sh
# project route: <project>/.grok/hooks/traceary.json
traceary hooks install --client grok --project-dir .

# user route: ~/.grok/hooks/traceary.json
traceary hooks install --client grok --global
```

Grok は project hook を独立した trust 境界として扱います。project route を使う場合は
ファイル内容を確認し、そのプロジェクトで Grok の `/hooks-trust` を実行してください。
project hook が存在する一方で host が project を未信頼と報告した場合、
`grok-hook-trust` が警告します。plugin hook を project route にコピーして代替しないで
ください。経路が重複すると同じイベントを二重に記録する可能性があります。

## 更新と削除

Traceary CLI と plugin は同じバージョンでリリースします。CLI の更新後は、対応する
tag を取得して installer を再実行します。

```sh
brew upgrade traceary
git fetch --tags
git checkout v0.23.0 # 導入済みTracearyのバージョンに置き換える
./scripts/install-grok-plugin.sh
traceary doctor --client grok --project-dir . --json
```

Grok のネイティブパッケージだけを削除する場合は次を実行します。

```sh
grok plugin uninstall traceary
```

hook-only で導入した project/global ファイルは plugin と独立しているため、使用した
場合は別途削除してください。

## トラブルシュート

| Doctor check | 意味と対応 |
| --- | --- |
| `grok-cli` が失敗 | Grok Build を導入し、`grok` を `PATH` に追加する |
| `grok-plugin` が警告 | パッケージを導入し直す。バージョン不一致の場合は同じ Traceary リリースのパッケージを使う |
| `grok-hook-trust` が警告 | project hook を確認して `/hooks-trust` を実行するか、未使用の project route を削除する |
| `grok-hooks` が警告 | 導入済み hook file が不足しているか、7 event の厳密な契約からずれている。plugin を再導入する |
| `grok-mcp` / `grok-skills` が警告 | 導入済みパッケージの内容が不足している。plugin を再導入する |
| `grok-event-coverage` が警告 | 直近の `agent=grok` event と待機中の hook/transcript queue を確認する。導入状態が正常でも実行時配送まで保証しない |

読み取り専用で確認できる command は次のとおりです。

```sh
grok plugin list --json
grok plugin details traceary
grok --cwd . inspect --json
traceary list --agent grok --limit 20
traceary doctor --client grok --project-dir . --json
```

Grok が最終メッセージをまだ追記していない場合、`Stop` の transcript 取得は意図的に
非同期になります。未処理 job は doctor が報告し、host hook を無期限に停止させません。
doctor の出力に生のプロンプトや transcript は含めません。

今後の作業は、[subagent の親子契約](https://github.com/duck8823/traceary/issues/1299)、
[未観測 lifecycle hook](https://github.com/duck8823/traceary/issues/1300)、
[公開 marketplace への掲載](https://github.com/duck8823/traceary/issues/1301)として
それぞれ独立して追跡します。

## パッケージ検証

maintainer は実プロジェクトを使わずに、リポジトリ内のパッケージと隔離した導入経路を
検証できます。

```sh
go run ./cmd/repo-tooling integrations verify
./scripts/smoke_test_integrations.sh grok
```

smoke test は一時 home を使ってパッケージを検証・導入し、`grok inspect` で
plugin / MCP / skill の内容を確認してから削除します。

## v0.23.0 dogfood 結果

2026-07-14 に Grok Build 0.2.99 で確認しました。

- 機密情報を除いた live core 実行で、native の `agent=grok` セッション1件に
  `session_started`、`prompt`、`command_executed`、`transcript` を記録し、完了後の
  transcript retry queue と hook spool は空になった
- 機密情報を除いた fixture 9件で、core route 5件、`PostToolUse` のファイル不在・拒否
  result variant、compact 前後の marker をカバーした
- 隔離した一時 home で install、inspect、doctor、uninstall が成功し、7件すべての
  `grok-*` check が `pass` になった
- 生の prompt、transcript、credential、hook target の private path、一時 workspace path を
  dogfood 証拠として commit していない
- external-agent policy gate が拒否したため subagent probe は実行せず、subagent の関連付けは
  推測で生成せず利用不可のままとした

最小化した実行記録は
[Issue #1279](https://github.com/duck8823/traceary/issues/1279#issuecomment-4961391647)に添付しています。

## 公式資料

- Grok Build hooks: https://docs.x.ai/build/features/hooks
- Grok Build skills、plugins、marketplaces: https://docs.x.ai/build/features/skills-plugins-marketplaces
