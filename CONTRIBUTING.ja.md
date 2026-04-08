# Traceary へのコントリビュート

[English](./CONTRIBUTING.md)

Traceary へのコントリビュートありがとうございます。
このガイドでは、ローカルセットアップ、検証手順、PR の進め方をまとめています。

## ローカルセットアップ

`go.mod` に記載している Go バージョンを使い、リポジトリを clone し、`main` から切った作業ブランチで進めてください。

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
python3 scripts/verify_docs_i18n.py
git diff --check
```

`make ci` を使うと、通常必要になるリポジトリ全体の検証をまとめて実行できます。

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

脆弱性の可能性がある場合は、まず公開 Issue を立てずに連絡してください。
連絡先は次のどちらかです。

- メール: `duck8823@gmail.com`
- GitHub の private vulnerability reporting（このリポジトリで有効な場合）

可能であれば、対象バージョンまたはコミット、再現手順や最小の PoC、想定される影響も添えてください。

Traceary はベストエフォートで保守しています。まず 7 日以内の受領連絡を目標にし、その後できるだけ早く修正方針を調整します。
