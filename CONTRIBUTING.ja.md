# Traceary へのコントリビュート

[English](./CONTRIBUTING.md)

Traceary へのコントリビュートありがとうございます。
このガイドでは、ローカルセットアップ、検証手順、PR の進め方をまとめています。

## ローカルセットアップ

`go.mod` に記載されている Go バージョンを使い、リポジトリを clone したうえで、`main` から切った作業ブランチで進めてください。

```sh
git clone https://github.com/duck8823/traceary.git
cd traceary
git switch -c your-topic-branch
```

## よく使う検証コマンド

PR を作る前、または更新する前に次を実行してください。

```sh
go test ./...
go tool golangci-lint run --timeout=5m
go run ./cmd/repo-tooling docs verify-i18n
git diff --check
```

`make ci` を使うと、通常必要になるリポジトリ全体の検証をまとめて実行できます。

## 並列テスト

`go test ./...` は既定で GOMAXPROCS 上限までパッケージを並列実行します。プロセス全体で共有する状態（環境変数、作業ディレクトリ、固定パス、golden 書き込み、lease reporter）に触るテストは直列のままにしてください。`t.Parallel()` を付けないでください。既知の直列 family：`t.Setenv` 駆動の hook テスト、`SetUserHomeDirFunc` を使うテスト、upgrade e2e テスト、lease / busy のタイミングテスト、golden writer。

CI は suite を gap なしの 3 shard に分けて並列実行します。`Test (sqlite)` が `./infrastructure/sqlite/`、`Test (cli)` が `./presentation/...`、`Test (rest)` が除外リスト方式で残り全パッケージを実行するため、新規パッケージの取りこぼしがありません。3 つとも必須チェックです。

直列 fallback と rollback：

```sh
go test -p 1 ./...   # 完全直列。最も遅いが競合が最小
```

並列負荷でのみ失敗するテストが出たら、テストを弱めるのではなく `t.Parallel()` を戻し、共有状態をコメントに残してください。

## 段階別テスト戦略

検証責務は段階で分割します。検証の削除ではなく、実行対象と頻度の分離です。

| 実行タイミング | 必須テスト |
|---|---|
| commit | staged 変更に対応する unit テスト。`scripts/test-select-staged.sh` が所有パッケージ＋逆依存に写像します。`scripts/install-git-hooks.sh` で hook を導入してください。docs のみの変更は文書検証を実行します。分類不能な変更は全量に拡大し、0 件成功扱いはしません。 |
| PR | 最終 head での全量 unit。CI の `Test (sqlite)` / `Test (cli)` / `Test (rest)` shard です。branch protection が fresh な実行を要求するため、古い結果の再利用はありません。 |
| wave | wave ごとに統合 head で E2E。`scripts/run-wave-e2e.sh --wave ID --ref REV` を使います。gate が通った wave だけが完了です。証跡は wave、SHA、時刻を束縛します。 |

分類：Go の `go test` パッケージが unit、`scripts/smoke_test_integrations.sh all` と live host gate が integration / E2E です。lint、security、release gate は CI で必須のまま黙って削除しません。変更には根拠を明示します。

## ドキュメントのルール

人向けの Markdown は、英語版と日本語版をセットで管理します。

- `README.md`、`CHANGELOG.md`、この文書のようなリポジトリ直下の文書には対応する `.ja.md` が必要です
- `docs/` 配下の文書も英語版 / 日本語版をそろえてください
- 文書を更新するときは、同じ PR で両方の言語版を更新してください

詳細は [docs/README.ja.md](./docs/README.ja.md) を参照してください。

## Pull request の進め方

変更を送るときは、次を守ってください。

1. `main` からブランチを切る
2. スコープを小さく保ち、レビューしやすい単位にする
3. できるだけ 1 コミット 1 関心事にする
4. 作業途中なら draft PR を使う
5. Motivation と実行した検証コマンドを PR に書く
6. merge 前に CI が通っていることを確認する

`main` へ直接 push しないでください。

## 脆弱性の連絡

[SECURITY.ja.md](./SECURITY.ja.md) を参照してください。
