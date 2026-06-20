# リリースガイド

[English](./README.md)

Traceary では、CLI 本体の公開導線を 3 つ用意し、あわせて release と同じ version の agent package も配布します。

## インストール

### `go install`

Go の bin directory に最新タグ版の CLI を入れたい場合は `go install` を使います。

```sh
go install github.com/duck8823/traceary@latest
```

特定の release を使いたい場合は tag を明示します。

```sh
go install github.com/duck8823/traceary@vX.Y.Z
```

### Homebrew

タグ付き release では、この repository の `main` にある `Formula/traceary.rb` も更新されるため、macOS では tap 形式の Homebrew formula として導入できます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### 事前ビルド済みバイナリ

タグ付き release では、次の圧縮バイナリを配布します。

- macOS amd64 / arm64
- Linux amd64 / arm64

GitHub Releases から自分の platform に合う archive を取得し、展開した `traceary` バイナリを `PATH` 上の directory に配置してください。

### ネイティブ連携パッケージ

タグ付き release では、Claude Code / Codex 向け package を repository 内で version をそろえて同じ release tag で管理・公開します。Gemini CLI extension archive（`traceary.tar.gz`）も既存 Gemini CLI 導入環境向けのレガシー互換として release asset に含まれます。

v0.21.1 以降、Traceary は文書化された公開 Antigravity hook/plugin surface に対する Antigravity plugin package（`integrations/antigravity-plugin/`）を提供します。hook の導入は `traceary hooks install --client antigravity`（workspace は `.agents/hooks.json`）または `--global`（`~/.gemini/config/hooks.json`）で行います。詳細は [Antigravity hooks / plugin ガイド](../integrations/antigravity.ja.md) を参照してください。

host ごとの install 手順は [ネイティブ連携ガイド](../integrations/README.ja.md) を参照してください。

## version metadata

Traceary は `traceary --version` で version metadata を表示します。

この metadata は次の 2 経路で埋まります。

- `go install github.com/duck8823/traceary@<tag>`: tagged module の Go build info
- release binary: `.goreleaser.yml` の GoReleaser `ldflags`

これにより、release build が既定の `dev` 表示のまま残るのを防ぎます。

## release 自動化

`.github/workflows/release.yml` は `v*` tag push で起動し、公式 GoReleaser GitHub Action を呼び出します。

このワークフローでは次を行います。

1. git history を完全取得する
2. Go をセットアップする
3. `go run ./cmd/repo-tooling release verify-changelog` で changelog coverage を検証する
4. tag ref では release mode、手動で branch から起動したときは snapshot mode で GoReleaser を実行する
5. tag release のときに GitHub Releases へ成果物とチェックサムを公開する
6. 保護された `main` へ直接 push せず、GitHub App installation token を使って Homebrew formula 更新用の専用 PR（`maintenance/homebrew-vX.Y.Z`）を作成または更新する
7. Gemini CLI extension archive (`traceary.tar.gz`) をレガシー互換として package して release asset に追加する
8. tag 付き GitHub Release が成功したあとで、対応する open 状態の親 release issue（`vX.Y.Z: ...`）を閉じる

`workflow_dispatch` は主にブランチ上でパイプラインを dry-run するために使いますが、`tag` input を渡せば既存の `v*` tag に対する release 完了処理の再実行にも使えます。

release 準備用 PR では metadata / docs / manifest をそろえて構いませんが、親 release issue を閉じてはいけません。親 issue は、tag 付き release workflow が成功するまで open のままにします。

`go run ./cmd/repo-tooling release verify-changelog` は CI の docs job からも必須で実行されます。このガードでは次を検証します。

- 現在の `VERSION` が `CHANGELOG.md` / `CHANGELOG.ja.md` の両方に存在すること
- 英語版と日本語版で release 見出しが一致していること
- 現在の `VERSION` 以下の `vX.Y.Z` git tag が両 changelog に反映されていること

この検証で過去の release gap も拾えるように、docs job は full history を checkout します。

## Release manifest verification

`scripts/verify_release_manifests.py` は marketplace metadata の drift がある release を止めます。確認内容は次の通りです。

- `.claude-plugin/marketplace.json` が存在し、`./integrations/claude-plugin` を指していること
- `.agents/plugins/marketplace.json` が存在し、`./plugins/traceary` を指していること
- `integrations/claude-plugin/.claude-plugin/plugin.json`、`integrations/gemini-extension/gemini-extension.json`、`plugins/traceary/.codex-plugin/plugin.json` の `version` が root の `VERSION` と一致すること

失敗した場合は、対象 release の `make release/bump VERSION=X.Y.Z` を実行するか、`VERSION` と該当 manifest を同時に更新してください。この check は CI、GoReleaser 前の `.github/workflows/release.yml`、および `make release/bump` で実行されます。

Homebrew formula は repository 直下の `Formula/` に置き、GoReleaser が自動生成します。手動編集は前提にしません。

`main` は保護されているため、release ワークフローは Homebrew 更新を direct push せず、専用 PR を開く方式にしています。

この Homebrew PR 経路を動かすには、repository level で次の設定が必要です。

- `HOMEBREW_APP_CLIENT_ID` を次のいずれかで設定する
  - repository variable
  - repository secret
- secret: `HOMEBREW_APP_PRIVATE_KEY`

GitHub App は `duck8823/traceary` のみに install し、repository permission は少なくとも次を付与します。

- `Contents`: read and write
- `Pull requests`: read and write

## Maintainer 向けリリース手順

GoReleaser workflow が artifact の公開を自動化しますが、maintainer のローカルで実行する作業も残ります。`vX.Y.Z` をリリースするときは、次の順番で進めてください。

1. **まず両方の changelog を更新する。** `CHANGELOG.md` と `CHANGELOG.ja.md` の双方に `## [vX.Y.Z] - YYYY-MM-DD` セクションを追加します。次のステップの前に両ファイルの release 見出しが一致していないと `go run ./cmd/repo-tooling release verify-changelog` が失敗します。
2. **manifest を bump する。** `make release/bump VERSION=X.Y.Z` を実行すると、`VERSION`・integration plugin manifest・`docs/landing/` のバージョン表示がまとめて更新され、`scripts/verify_release_manifests.py` と `go run ./cmd/repo-tooling integrations verify` も走ります。
3. **ローカル検証。** `go run ./cmd/repo-tooling release verify-changelog` / `python3 scripts/verify_release_manifests.py` / `go run ./cmd/repo-tooling docs verify-landing` / `go test ./...` / `go tool golangci-lint run` をすべて通します。残りの Python entrypoint は `cmd/repo-tooling` へ移行中で、移行順は [`../operations/repo-tooling.ja.md`](../operations/repo-tooling.ja.md) に整理しています。
4. **cockpit を dogfood する。** `traceary tui` を変更する release では、tag 前に `go test ./presentation/cli -run 'TestCockpitDogfood'` を実行し、80x24 smoke を含む [`cockpit dogfood checklist`](../operations/cockpit-dogfood.ja.md) を完了します。
5. **landing page をプレビューする。** `python3 -m http.server --directory docs/landing 8000` を起動して `http://localhost:8000/` を開き、hero の version eyebrow と brew install の terminal アニメーションが新バージョンになっているかを確認します。`Pages` workflow は GitHub Release の publish 時に自動再 deploy するため、ここが本番反映前の最後のチェックポイントです。
6. **release-preparation PR を開く。** `maintenance/release-vX.Y.Z` ブランチを作成し、changelog と bump を commit・push して `main` 向けに PR を開きます。**`Closes #<parent>` は書かない**でください。親 release issue は release workflow が閉じるため、release-prep PR からは閉じません。
7. **レビュー + merge。** release-prep PR も通常の PR と同じレビューゲートを通します。v0.21.1 のゲートは **Claude レビュー + ローカル dogfood + tests/CI** です。
   - Gemini レビューは**不要**です（Gemini CLI は retired / 利用不可）。
   - Codex の実装・レビューは、ローカルポリシーやユーザー指示で無効化されている場合は**不要**です。Codex が有効かつ利用可能な場合にのみ Codex app review を取得します。
   - 最新 head に対する Claude レビューを取得し、ローカル dogfood と `go test ./...` / `golangci-lint` / CI が green であることを確認してから merge commit でマージします。
   - Antigravity は v0.21.1 以降サポート対象であり、`integrations/antigravity-plugin/` を同梱します。doctor は `antigravity-capability` と `antigravity-config` を報告します（上記「ネイティブ連携パッケージ」を参照）。
8. **tag を打って push する。** release-prep PR が merge されたら、`git checkout main && git pull --ff-only && git tag vX.Y.Z && git push origin vX.Y.Z` を実行します。`v*` tag が `.github/workflows/release.yml` を起動します。
9. **release workflow を監視する。** tag run に対して `gh run watch` を実行し、成功後に `gh release view vX.Y.Z` で公開を確認します。GitHub Release が publish されると `.github/workflows/pages.yml` も発火し、`docs/landing/` を GitHub Pages に再 deploy します。その run が成功し、`https://duck8823.github.io/traceary/`（CNAME 経由で `https://duck8823.net/traceary/`）が新バージョンを反映していることを確認してください。
10. **Homebrew formula PR を確認する。** release workflow が `maintenance/homebrew-vX.Y.Z` PR を開いて auto-merge を有効化します。実際に merge されたか確認し、`brew update && brew upgrade traceary && traceary -v` で新バージョンを確かめます。
11. **親 release issue が自動で閉じたか確認する。** workflow は GitHub Release 公開後に対応する `vX.Y.Z: ...` 親 issue を閉じます。残っている場合は、その段階で workflow が失敗しています。

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
