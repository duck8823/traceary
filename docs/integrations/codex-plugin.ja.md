# Codex plugin

[English](./codex-plugin.md)

Codex 向け package は `plugins/traceary/` にあります。
Codex では、MCP / skill / slash command を載せる plugin 本体と、自動記録に使う `~/.codex/hooks.json` / `codex_hooks` の両方が必要になるため、Traceary ではそれをまとめて入れる `traceary integration codex ...` コマンドを用意しています。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart`, `UserPromptSubmit`, `Stop`, `PostToolUse` hook
- slash command の `/traceary:help` と `/traceary:doctor`
- 文脈で効く `traceary-session-history` skill

## ローカル checkout から install する

Codex は現時点で Claude や Gemini のような公開 install CLI を持たないため、Traceary では plugin 本体と hooks をまとめて入れる専用の `traceary integration codex ...` 導線を用意しています。

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

3. package 済み plugin を install し、Codex config と hooks もまとめて設定します。

```sh
cd ~/src/traceary
traceary integration codex install
```

既定では、このコマンドが次を行います。

- `plugins/traceary/` を `~/.agents/plugins` 配下の local marketplace にコピー
- `~/.codex/plugins/cache/local-traceary-plugins/traceary/local` に active plugin cache を配置
- `~/.codex/config.toml` の `[plugins."traceary@local-traceary-plugins"]` を有効化
- `[features].codex_hooks = true` を有効化
- `~/.codex/hooks.json` に Traceary 用の hook 設定をマージ

`traceary` が `PATH` にいない場合は `--traceary-bin /absolute/path/to/traceary` を付けてください。repository 外から実行する場合は `--repo-root /path/to/traceary` も指定できます。

## Update

```sh
cd ~/src/traceary
git pull --ff-only
traceary integration codex install
```

## アンインストール

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

アンインストールでは Traceary の plugin cache、plugin config entry、Traceary が管理する Codex hook entry を外します。`[features].codex_hooks` は、他の hook 利用を壊さないため残します。

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

Codex 向け smoke test は、CLI が作る plugin cache・config・hooks の状態を確認します。認証済みの環境で runtime probe まで見たい場合は `TRACEARY_ENABLE_CODEX_RUNTIME_SMOKE=1` を付けてください。
