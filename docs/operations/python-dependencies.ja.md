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

| 対象 | 現在の entrypoint | いま Python を使っている理由 | 今後の方向 |
| --- | --- | --- | --- |
| Codex local plugin install | `python3 scripts/codex/install_plugin.py` | plugin file / config / hook wiring を 1 つの helper でまとめている | first-class な Go entrypoint に置き換える |
| Codex local plugin uninstall | `python3 scripts/codex/uninstall_plugin.py` | plugin cache / config / Traceary 管理下 hook をまとめて外している | 対になる Go entrypoint に置き換える |

### maintainer-only

| 対象 | 現在の entrypoint | 利用箇所 | 今後の方向 |
| --- | --- | --- | --- |
| docs pairing verification | `python3 scripts/verify_docs_i18n.py` | ローカル検証、CI docs job | 当面維持。将来的には Go 製 repo verifier に統合 |
| integration package verification | `python3 scripts/verify_integrations.py` | release prep、smoke test、CI | Codex install 経路の移行後に Go へ移す |
| changelog coverage verification | `python3 scripts/verify_changelog_releases.py` | release prep、CI docs/release jobs | 共通の Go verifier ができた段階で統合する |
| version bump helper | `python3 scripts/bump_version.py` | release prep | user 影響が低いので最後に移す |

## この issue の対象外

次は、この issue が扱う Python 依存の話とは分けます。

- `scripts/hooks/` 配下の shell wrapper
  - これは hook runtime cleanup の別 issue で扱う
- support 対象外の one-off local developer script
- Traceary が配布していない third-party client tooling

## 推奨する移行順

### 1. Codex install / uninstall helper

最優先はここです。理由は、公開 docs に載っている user-facing な導線であり、現在も local Codex integration の完全な利用手順で Python を要求しているためです。

置き換え先の第一候補:

- `traceary integration codex install`
- `traceary integration codex uninstall`

これを先にやる理由:

- user-facing な前提条件から Python を外せる
- install behavior を通常の Go CLI surface に戻せる
- 将来の docs が repository-local helper ではなく `traceary` 自体を案内できる

### 2. integration verification

public Codex flow を移したあとは、`scripts/verify_integrations.py` の移行が一番効果的です。
release prep、smoke test、CI のすべてで使っているためです。

推奨方向:

- integration manifest と local package structure を検証できる Go 製 repo verification command を用意する
- 実装の置き場所は repository-only helper package / subcommand でもよいが、利用者向けの documented entrypoint は 1 つに保つ

### 3. changelog / docs verifier

その次に移す対象:

- `scripts/verify_changelog_releases.py`
- `scripts/verify_docs_i18n.py`

これらは maintainer-only なので、優先度より correctness を重視します。
共通の Go verifier ができたなら、別々の tool を増やさずそこへ統合します。

### 4. version bump helper

`scripts/bump_version.py` は便利ですが優先度は低めです。
上の高優先度項目が固まってから移せば十分です。

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
