# Grok Build plugin

[English](./grok-plugin.md)

Traceary v0.23.0 は Grok Build をネイティブにサポートします。
[`integrations/grok-plugin/`](../../integrations/grok-plugin/) のパッケージは、
検証済みのライフサイクル hook 7件、ローカルの Traceary MCP server 1件、
共通のメモリ・セッション skill 3件を導入します。hook 由来のイベントは
`client=hook`、`agent=grok` として記録します。

## 対応範囲

このパッケージは、Grok Build 0.2.99 で実際に確認した契約を対象にします。

| Grok event | Traceary の処理 |
| --- | --- |
| `SessionStart` | Grok セッションを開始または更新する |
| `UserPromptSubmit` | ユーザープロンプトを記録する |
| `PreToolUse` | 完了済み監査を書き込まず、tool payload を検証する |
| `PostToolUse` | 完了したコマンド監査を1件記録する。確認済みのファイル不在・拒否結果も失敗として扱う |
| `Stop` | `updates.jsonl` から可能な範囲で transcript を記録し、turn 境界を作る。セッションは終了しない |
| `PreCompact` / `PostCompact` | compact 前後の marker を別々に記録する。Grok は summary 本文を公開しない |

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

2. CLI と同じ Traceary リリースを取得し、ネイティブパッケージを導入します。
   installer はパッケージを検証し、既存の `traceary` plugin を置き換えた後、
   導入済みの内容を表示します。

```sh
git clone --branch v0.23.0 --depth 1 https://github.com/duck8823/traceary.git
cd traceary
./scripts/install-grok-plugin.sh
```

installer は `grok plugin install --trust` を実行します。信頼した command hook は
ローカルで実行されるため、実行前に取得したパッケージを確認してください。現行の
パッケージは文書化された Traceary hook entrypoint だけを呼び出し、Grok の認証情報や
ブラウザ状態を読み取ったり送信したりしません。

3. Grok で開くプロジェクトから、有効な導入状態を確認します。

```sh
traceary doctor --client grok --project-dir . --json
```

正常な導入では `grok-cli`、`grok-plugin`、`grok-hook-trust`、`grok-hooks`、
`grok-mcp`、`grok-skills` が `pass` になります。別の
`grok-event-coverage` check は直近のデータベース証拠を評価します。直近の
セッションが3件未満の場合は、誤って成功とせず「まだ判定しない」と報告します。

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
