# バイナリ upgrade 後の plugin 更新チェックリスト

[English](./post-upgrade-plugins.md)

#1361 · 親カット #1360。

Homebrew / `go install` / release binary の upgrade は **Traceary CLI バイナリだけ**を更新します。ホスト plugin パッケージ（Claude / Codex / Gemini / Antigravity / Grok）は各ホストの cache / install root にあり、`brew upgrade traceary` では **自動更新されません**。

新しい minor/patch へバイナリを上げたあとは毎回:

1. `traceary -v` で binary を確認
2. 利用ホストごとに `traceary doctor --client <host> --json`
3. `*-plugin-version` の WARN を下表のコマンドで解消
4. plugin-version が **PASS** または明確な理由の **SKIP** になるまで再実行

## ホスト別 refresh

| Host | 典型 install root | refresh コマンド |
|---|---|---|
| Claude Code | Claude plugin cache + marketplace | Claude Code 内: Traceary marketplace key に対する `claude plugins update`（doctor の `FixCommand` が正確な key を出す） |
| Codex | `~/.codex/plugins/cache/.../traceary/` | **matching release tag** の checkout から再インストール（`plugins/traceary/` ドキュメント参照） |
| Gemini CLI（レガシー extension） | `~/.gemini/extensions/traceary/` | `gemini extensions update traceary` |
| Antigravity | `~/.gemini/config/plugins/traceary` および/または `~/.gemini/antigravity-cli/plugins/traceary` | matching checkout で `agy plugin install integrations/antigravity-plugin` |
| Grok Build | `scripts/install-grok-plugin.sh` 参照 | matching checkout で `./scripts/install-grok-plugin.sh` |

doctor の `*-plugin-version` は既知の非対話 path があるとき `FixCommand` を出します。推測でフラグを増やさないでください。

## Antigravity の dual path

Antigravity は次の両方にパッケージが残ることがあります。

- `~/.gemini/config/plugins/traceary`
- `~/.gemini/antigravity-cli/plugins/traceary`

**一方**が実行中 binary と一致し、もう一方が不完全（`version` 欠落）な場合、doctor は不完全側の恒久 WARN を **skip** します（#1361）。`traceary doctor --client antigravity` で hook を確認したうえで、未使用ディレクトリは削除して構いません。

## Homebrew 注意

`brew upgrade traceary` は host plugin cache を書き換えません。dogfood 機では plugin refresh を必須の post-upgrade 手順として扱ってください。

## 関連

- [リリースガイド](./README.ja.md)
- [Integrations 概要](../integrations/README.ja.md)
