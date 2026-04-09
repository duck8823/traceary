# リリースガイド

[English](./README.md)

Traceary では、CLI 本体の公開導入方法を 3 つ用意し、あわせて release と同期した agent package も配布します。

## インストール

### `go install`

Go の bin directory に最新タグ版 CLI を入れたい場合は `go install` を使います。

```sh
go install github.com/duck8823/traceary@latest
```

特定の release を使いたい場合は、tag を明示します。

```sh
go install github.com/duck8823/traceary@v0.1.17
```

### Homebrew

tagged release では、この repository の `main` branch にある `Formula/traceary.rb` も更新されるため、macOS では tap 形式の Homebrew formula として導入できます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### 事前ビルド済みバイナリ

タグ付き release では、次の圧縮バイナリを配布します。

- macOS amd64 / arm64
- Linux amd64 / arm64

GitHub Releases から自分の platform に合う archive を取得し、展開した `traceary` binary を `PATH` 上の directory に配置してください。

### ネイティブ連携パッケージ

tagged release では、Gemini CLI extension 用の `traceary.tar.gz` も公開します。
Claude Code / Codex 向け package は repository 内で version を揃え、同じ release tag で管理します。

host ごとの install 手順は [ネイティブ連携ガイド](../integrations/README.ja.md) を参照してください。

## version metadata

Traceary は `traceary --version` で version metadata を表示します。

この metadata は次の 2 経路で埋まります。

- `go install github.com/duck8823/traceary@<tag>`: tagged module の Go build info
- release binary: `.goreleaser.yml` の GoReleaser `ldflags`

これにより、release build が既定の `dev` 表示のまま残ることを防ぎます。

## release 自動化

`.github/workflows/release.yml` は `v*` tag push で起動し、公式 GoReleaser GitHub Action を呼び出します。

このワークフローでは次を行います。

1. git history を完全取得する
2. Go をセットアップする
3. tag ref では release mode、手動で branch から起動したときは snapshot mode で GoReleaser を実行する
4. tag release のときに GitHub Releases へ成果物とチェックサムを公開する
5. Homebrew 向けに `main` branch の `Formula/traceary.rb` を更新する
6. Gemini CLI extension archive (`traceary.tar.gz`) を package して release asset に追加する

`workflow_dispatch` は主にブランチ上でパイプラインを dry-run するためのものです。実際に公開リリースしたい場合は `v*` tag を push するか、tag ref を指定して手動実行してください。

Homebrew formula は repository 直下の `Formula/` に置き、GoReleaser が自動生成します。手動編集は前提にしません。

この Homebrew step は、release ワークフローが `main` へ push できることを前提にしています。将来、bot の直接 push を禁止する ブランチ保護 を追加した場合は、次の tag を出す前に GoReleaser の brew target を見直してください。

## ローカル snapshot build

tag を打つ前に release artifact を確認したい場合は、ローカル snapshot target を使います。

```sh
make release/snapshot
```

これは `goreleaser release --snapshot --clean` を実行し、artifact を `dist/` に出力します。

Gemini extension archive もローカルで確認したい場合は、次を使います。

```sh
make release/gemini-extension
```

## 参考

- GitHub Releases: https://github.com/duck8823/traceary/releases
- GoReleaser GitHub Actions docs: https://goreleaser.com/customization/ci/actions/
- GoReleaser install docs: https://goreleaser.com/getting-started/install/
