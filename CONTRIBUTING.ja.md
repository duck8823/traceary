# Traceary へのコントリビューション

[English](./CONTRIBUTING.md)

Traceary へのコントリビューションありがとうございます。
このガイドでは、想定するローカルセットアップ、検証手順、pull request の進め方を説明します。

## ローカルセットアップ

Traceary は Go 製の CLI / MCP server です。
`go.mod` に書かれている Go version を使い、repository を clone したうえで `main` から feature branch を切って作業してください。

典型的なセットアップ例:

```sh
git clone https://github.com/duck8823/traceary.git
cd traceary
git switch -c your-topic-branch
```

## よく使うコマンド

pull request を作る前、または更新する前に次を実行してください。

```sh
go test ./...
go tool golangci-lint run --timeout=5m
python3 scripts/verify_docs_i18n.py
git diff --check
```

`make ci` でも、通常必要な repository レベルの検証をまとめて実行できます。

## ドキュメント規則

Traceary の人間向け Markdown は、英語版と日本語版のペアで管理します。

- `README.md`, `CHANGELOG.md`, このガイドのような repository 直下の文書には対応する `.ja.md` が必要です
- `docs/` 配下の文書も英語版 / 日本語版のペアにしてください
- 文書を更新するときは、同じ pull request で両方の言語版を更新してください

詳細は [`docs/README.ja.md`](./docs/README.ja.md) を参照してください。

## Pull request の期待値

変更を送るときは、次を守ってください。

1. `main` から branch を切る
2. scope を小さく保ち、レビューしやすい単位にする
3. できるだけ 1 commit 1 concern にする
4. 作業途中の段階では draft PR を使う
5. motivation と実行した検証コマンドを PR に含める
6. merge 前に CI が通ることを確認する

`main` へ直接 push しないでください。

## CI の期待値

現在の GitHub Actions では次を検証します。

- Go test
- `golangci-lint`
- 文書の言語ペア検証

reviewer が回避可能な失敗を再発見しなくて済むよう、ローカル検証はできるだけ CI に合わせてください。
