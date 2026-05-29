# Python 依存の棚卸しと縮小計画

[English](./python-dependencies.md)

Traceary の core runtime は Go ですが、いくつかの repository workflow では今も `python3` を使っています。
この文書では、その依存がどこに残っているのか、誰に影響するのか、そして次にどこから減らすべきかを整理します。

## 現在の方針

- `traceary` CLI と MCP server は、Python なしで動く状態を維持する
- user-facing な install / runtime workflow に Python 前提を増やす場合は、明示的な設計判断を必須にする
- maintainer-only の Python helper は当面許容するが、所有者と移行先を文書で明示する

## 現在 Python に依存している面

### user-facing

現在、support 対象の user-facing install / runtime flow で `python3` を必須にするものはありません。
Codex の唯一サポートされる install path は Codex CLI 公式の `/plugins` flow です（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary Plugins` → `Traceary`）。`traceary integration codex install` helper は v0.14.0 で廃止され、cleanup 専用 `traceary integration codex uninstall` surface は v0.15.0 で削除されました。どちらの retired path も `python3` には依存しません。今後は `/plugins` と Codex plugin ガイドの手動 cleanup 手順で legacy state を片付けてください。

### maintainer-only

文書化された repo-tooling 移行順は**完了**しました — 計画上の helper はすべて `go run ./cmd/repo-tooling ...` 経由になりました（下記移行順を参照）。release/CI のガードのうち、計画外で Python が残るものは次のとおりです:

| 対象 | 現在の entrypoint | 利用箇所 | 今後の方向 |
| --- | --- | --- | --- |
| release manifest verification | `python3 scripts/verify_release_manifests.py` | release prep、CI integrations/release jobs | 当初計画外。再検討時に `cmd/repo-tooling` へ統合 |
| removed-alias doc guard | `python3 scripts/verify_docs_no_removed_aliases.py` | CI docs job | 当初計画外 |
| release-drafter workflow guard | `python3 scripts/verify_release_drafter_workflow.py` | CI docs job | 当初計画外 |

## この issue の対象外

次は、この issue が扱う Python 依存の話とは分けます。

- `scripts/hooks/` 配下の shell wrapper
  - これは hook runtime cleanup の別 issue で扱う
- support 対象外の one-off local developer script
- Traceary が配布していない third-party client tooling

## 推奨する移行順

### 1. integration verification — ✅ 完了 (v0.20.0)

`scripts/verify_integrations.py` は `go run ./cmd/repo-tooling integrations verify` に置き換えて削除しました。CI・Makefile（`integrations/check`、`release/bump`）・integration smoke test はこの Go entrypoint を使います。

- 以降の verifier も、単発 helper ではなく同じ `cmd/repo-tooling` entrypoint に集約する

### 2. changelog / docs verifier

その次に移す対象:

- ~~`scripts/verify_changelog_releases.py`~~ → ✅ `go run ./cmd/repo-tooling release verify-changelog`（完了、v0.20.0）
- ~~`scripts/verify_docs_i18n.py`~~ → ✅ `go run ./cmd/repo-tooling docs verify-i18n`（完了、v0.20.0）
- ~~`scripts/verify_landing.py`~~ → ✅ `go run ./cmd/repo-tooling docs verify-landing`（完了、v0.20.0）

これらは maintainer-only なので、優先度より correctness を重視します。
共通の Go verifier ができたなら、別々の tool を増やさずそこへ統合します。

### 3. version bump helper — ✅ 完了 (v0.20.0)

`scripts/bump_version.py` は `go run ./cmd/repo-tooling release bump-version --version X.Y.Z` に置き換えて削除しました。`make release/bump` は Go コマンドを使います。これで文書化された移行順は完了です。

## 今後の repository rule

上の移行が終わるまでは、次をルールにします。

1. 新しい user-facing Python helper command は追加しない
2. maintainer-only の Python helper をどうしても追加する場合は、少なくとも次を文書化する
   - なぜ今 Go を使わないのか
   - どこから呼ばれるのか
   - 将来どこへ移すのか
3. 単発 script を増やすより、既存 verifier の拡張を優先する

## 関連文書

- architecture principles: [`../architecture/README.ja.md`](../architecture/README.ja.md)
- Codex integration: [`../integrations/codex-plugin.ja.md`](../integrations/codex-plugin.ja.md)
- release workflow: [`../release/README.ja.md`](../release/README.ja.md)
