# Codex plugin

[English](./codex-plugin.md)

Codex 向け package は `plugins/traceary/` にあり、repository root の `.agents/plugins/marketplace.json` を配布基点にした Codex marketplace 形式で管理します。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart`, `Stop`, `PostToolUse` hook
- slash command の `/traceary:help` と `/traceary:doctor`
- 文脈で効く `traceary-session-history` skill

## ローカル checkout から install する

Codex は現時点で Claude や Gemini のような公開 install CLI を持たないため、Traceary では標準 local plugin directory 向け helper script を同梱します。

1. 先に Traceary CLI を入れます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. この repository を安定した場所に clone します。

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
```

3. package 済み plugin を `~/.agents/plugins` に install します。

```sh
cd ~/src/traceary
python3 scripts/codex/install_plugin.py
```

この command は `plugins/traceary/` を標準 local plugin directory へコピーし、対応する marketplace entry も upsert します。

## Update

```sh
cd ~/src/traceary
git pull --ff-only
python3 scripts/codex/install_plugin.py
```

## Uninstall

```sh
cd ~/src/traceary
python3 scripts/codex/uninstall_plugin.py
```

## Doctor と smoke test

実運用の確認は次を基本にします。

```sh
traceary doctor --client codex --json
```

package 自体の構造検証は次です。

```sh
python3 scripts/verify_integrations.py
```

この repository からの smoke test は次です。

```sh
./scripts/smoke_test_integrations.sh codex
```

Codex 向け smoke test は、既定では package 済み marketplace/plugin layout の検証を行い、plugin-enabled build がある場合だけ runtime probe を追加で試します。
