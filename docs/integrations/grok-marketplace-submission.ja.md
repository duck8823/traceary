# Grok public marketplace 投稿手順

[English](./grok-marketplace-submission.md)

#1301 · 親カット #1360。

## 目的

Traceary の native Grok パッケージ（`integrations/grok-plugin/`）を
[xAI Plugin Marketplace](https://github.com/xai-org/plugin-marketplace) に掲載し、
clone なしで導入できるようにする。一方で
`./scripts/install-grok-plugin.sh` は決定的なローカル fallback として残す。

## カタログエントリ

1. パッケージを含む Traceary commit（release tag 推奨）で:

```sh
./scripts/generate-grok-marketplace-entry.sh v0.28.0
# 準備中は HEAD でも可
./scripts/generate-grok-marketplace-entry.sh HEAD
```

2. 出力 JSON を `xai-org/plugin-marketplace` の
   `.grok-plugin/marketplace.json` → `plugins[]` に追加する。

3. そのリポジトリで:

```sh
python3 scripts/generate-plugin-index.py
python3 scripts/validate-catalog.py
```

4. PR を開く。CI が catalog を検証し、xAI の code-owner レビューが必要。

### Source 契約

| Field | Value |
|---|---|
| `source.url` | `https://github.com/duck8823/traceary.git` |
| `source.sha` | タグ付け release の 40 文字 commit SHA |
| `source.path` | `integrations/grok-plugin` |
| `name` | `traceary` |

秘密は commit しない。パッケージは Traceary hook と local MCP stdio のみ。

## 投稿前のローカル検証

```sh
go run ./cmd/repo-tooling integrations verify
./scripts/verify-grok-plugin-clean-home.sh
```

clean-home は一時 `HOME` で install / update (reinstall) / details / uninstall を確認する。

## marketplace merge 後

1. host UI の install コマンドが安定していれば release notes に追記。
2. Traceary の各 release ごとに marketplace 側で **`sha`（と `version`）を bump** する follow-up PR。
3. ローカル install は offline / tag pin 用に常にドキュメントへ残す。

## 関連

- [Grok Build plugin ガイド](./grok-plugin.ja.md)
- テンプレート: [`../../integrations/grok-plugin/marketplace-entry.json`](../../integrations/grok-plugin/marketplace-entry.json)
