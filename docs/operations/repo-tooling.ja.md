# repository tooling の入口と移行方針

[English](./repo-tooling.md)

Traceary の user-facing runtime は、配布している Go CLI と MCP server に閉じているべきです。
一方で、maintainer-only の repository helper は別種の tooling です。runtime の product surface ではありませんが、置き場所と入口は統一する必要があります。

この文書では、その入口、Python helper からの移行順、そして新しい repository automation を増やすときの原則を定義します。

## 決定事項

maintainer-only の repository tooling は、専用の Go entrypoint に寄せます。

- `go run ./cmd/repo-tooling ...`

repository verifier、release preparation helper、repository 内だけで成立する structure check は、この面に集約する方針です。

## なぜ main の `traceary` CLI に入れないのか

main の `traceary` binary は public runtime entrypoint です。
ここに入るべきものは次です。

- user-facing CLI command
- support 対象の hook runtime entrypoint
- MCP server

maintainer-only の repository automation は性質が異なります。

- Git checkout を前提にしてよい
- repository にしか無い file を検証してよい
- install 済み product の一部ではなくても、CI や release preparation では必要になる

そのため、runtime behavior と repository maintenance concern を混ぜずに済む `cmd/repo-tooling` を選びます。

## repo tooling に入れるもの

`cmd/repo-tooling` に置く例:

- integration package verification
- docs i18n pairing check
- changelog coverage check
- version bump のような release-prep helper

逆に、ここへ入れないもの:

- `traceary hook ...` runtime subcommand
- end-user 向け install / uninstall flow
- MCP server の runtime behavior
- support 対象外の one-off personal script

## 想定する command 形

最終的には、次のような subcommand に寄せます。

- `go run ./cmd/repo-tooling integrations verify`
- `go run ./cmd/repo-tooling integrations sync-hooks`
- `go run ./cmd/repo-tooling docs verify-i18n`
- `go run ./cmd/repo-tooling docs verify-antigravity-status`
- `go run ./cmd/repo-tooling release verify-changelog`
- `go run ./cmd/repo-tooling release bump-version --version X.Y.Z`

実際の package layout は調整してよいですが、documented entrypoint は 1 つに揃えます。

## 移行順

### 1. integration verification — ✅ 移行済 (v0.20.0)

最初の対象（完了）:

- ~~`scripts/verify_integrations.py`~~ → `go run ./cmd/repo-tooling integrations verify`

これを先にやる理由:

- CI / smoke test / release preparation のすべてで使っている
- 複数 integration package と managed file をまたいで検証している
- 検証対象の Go integration logic の近くへ寄せる価値が高い

Go 入口は Python のチェック（canonical hook コピー、Claude / Codex / Gemini の manifest + managed file、Codex の削除済みコマンド stub、docs i18n pair）を再現し、CI（`.github/workflows/ci.yml`）と Makefile（`integrations/check`、`release/bump`）に配線済み。Python スクリプトは削除しました。

### 2. docs pairing verification — ✅ 移行済 (v0.20.0)

次の対象（完了）:

- ~~`scripts/verify_docs_i18n.py`~~ → `go run ./cmd/repo-tooling docs verify-i18n`

Go 入口は en/ja pairing と冒頭の言語切り替えチェックを再現し、CI（`.github/workflows/ci.yml`）・Makefile（`docs/check`）・`CONTRIBUTING` に配線済み。Python スクリプトは削除しました。

### 3. changelog coverage verification — ✅ 移行済 (v0.20.0)

3 番目の対象（完了）:

- ~~`scripts/verify_changelog_releases.py`~~ → `go run ./cmd/repo-tooling release verify-changelog`

Go 入口は en/ja changelog の整合・現 VERSION・released tag coverage を再現し、CI（`.github/workflows/ci.yml`）と release workflow（`.github/workflows/release.yml`）に配線済み。Python スクリプトは削除しました。

### 4. landing page version drift verification — ✅ 移行済 (v0.20.0)

4 番目の対象（完了）:

- ~~`scripts/verify_landing.py`~~ → `go run ./cmd/repo-tooling docs verify-landing`

Go 入口は hero-eyebrow（major.minor）と Homebrew bottle / Cellar（full X.Y.Z）のバージョンずれ検証を再現し、CI（`.github/workflows/ci.yml`）・release workflow（`.github/workflows/release.yml`）・Makefile（`landing/check`、`release/bump`）に配線済み。Python スクリプトは削除しました。

### 5. version bump helper — ✅ 移行済 (v0.20.0)

最後の対象（完了）:

- ~~`scripts/bump_version.py`~~ → `go run ./cmd/repo-tooling release bump-version --version X.Y.Z`

Go 入口は VERSION / plugin manifest / landing marker の書き換えを再現し、`make release/bump` が呼びます。Python スクリプトは削除し、この移行順を完了しました。

## 今後のルール

上の移行が終わるまでは、次を repository rule にします。

1. 新しい maintainer-only Python helper を増やす場合は、issue に「なぜ今 Go ではないのか」を明記する
2. 単発 script を増やすより、既存 verifier の拡張を優先する
3. support 対象の maintainer helper は docs index と関連 workflow guide から辿れるようにする
4. user-facing entrypoint は `traceary` に、repository-only helper は repo tooling に置く

## 現在の状態

上記 5 つの移行ステップはすべて完了しました (v0.20.0): integration / docs-i18n / changelog / landing の検証と version-bump helper はすべて `go run ./cmd/repo-tooling ...` 経由になり、CI・Makefile・release workflow・CONTRIBUTING を配線済み。release/CI のガード数本（例: `verify_release_manifests.py`）は Python のまま残りますが、これらは本移行計画の対象外です — [`python-dependencies.ja.md`](./python-dependencies.ja.md) を参照。この文書は、今後の maintainer automation が場当たり的な script を増やさず一つの一貫した surface に収束するための合意先を定義します。

## 関連文書

- Python 依存の棚卸し: [`./python-dependencies.ja.md`](./python-dependencies.ja.md)
- release workflow: [`../release/README.ja.md`](../release/README.ja.md)
- integrations overview: [`../integrations/README.ja.md`](../integrations/README.ja.md)
