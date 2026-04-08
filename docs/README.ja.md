# 文書ガイド

[English](./README.md)

Traceary の人間向けドキュメントは、英語版と日本語版のペアで管理します。
このガイドは、repository 内の文書命名規則と運用ルールを定義します。

## 対象範囲

このルールの対象:

- `README.md` や `CHANGELOG.md` のような repository 直下の Markdown 文書
- `docs/` 配下の Markdown 文書

このルールの対象外:

- JSON / YAML / SQL などの機械可読ファイル
- `examples/` 配下のサンプル
- 生成物や third-party の vendored file

## 命名規則

英語版を基本ファイル名とし、日本語版は `.ja.md` suffix を付けます。

例:

- `README.md` ↔ `README.ja.md`
- `CHANGELOG.md` ↔ `CHANGELOG.ja.md`
- `docs/hooks/README.md` ↔ `docs/hooks/README.ja.md`
- `docs/cli/search.md` ↔ `docs/cli/search.ja.md`

## ペア作成の必須ルール

対象範囲の人間向け Markdown 文書を追加・改名するときは、次を守ります。

1. 英語版と日本語版を同じ変更で追加する
2. 相対 path と basename は揃える
3. 差分は language suffix (`.ja.md`) だけにする

`main` に入った時点で片方だけ欠けた状態を残さないでください。

## 言語切り替えリンク

ペアになっているすべての文書は、先頭付近に言語切り替えリンクを置きます。

- 英語版は `[日本語](...)`
- 日本語版は `[English](...)`

強い理由がない限り、top-level title の直下に置いてください。
リンクは `./README.md` のような同一ディレクトリ相対 path を使ってください。

## 更新ルール

片方の言語版を更新するときは:

- 同じ pull request で他方も更新する
- 見出し、例、version 参照を揃える
- 表現は読みやすさのために多少変えてよいが、内容は drift させない

## CI による検証

`python3 scripts/verify_docs_i18n.py` では次を確認します。

- 対象範囲の英語 Markdown に日本語ペアがあること
- 対象範囲の日本語 Markdown に英語ペアがあること
- 各ファイルの先頭付近に期待する language switch link があること

GitHub Actions でも同じ検証を行います。

## 現在の文書セット

- 全体概要: `README.md` / `README.ja.md`
- コントリビューションガイド: `CONTRIBUTING.md` / `CONTRIBUTING.ja.md`
- release 履歴: `CHANGELOG.md` / `CHANGELOG.ja.md`
- hooks ガイド: `docs/hooks/README.md` / `docs/hooks/README.ja.md`
- release ガイド: `docs/release/README.md` / `docs/release/README.ja.md`
