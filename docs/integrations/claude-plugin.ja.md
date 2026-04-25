# Claude Code plugin

[English](./claude-plugin.md)

Claude 向け package は `integrations/claude-plugin/` にあり、repository root の `.claude-plugin/marketplace.json` から配布します。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart` / `SessionEnd` hook
- `Bash` / `mcp__.*` / 組み込み tool matcher (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`) 向けの `PostToolUse` / `PostToolUseFailure` audit hook
- slash command として使える `/traceary-help` と、文脈で自動適用される `traceary-session-history` / `traceary-memory-capture` skill（後者は decision / constraint / lesson / preference / artifact を発見したときにエージェントが `manage_memory(action="propose")` を能動的に呼ぶよう誘導します）

## Install

1. 先に Traceary CLI を入れます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. この repository を Claude marketplace として追加します。

```sh
/plugin marketplace add duck8823/traceary
```

3. その marketplace から Traceary plugin を install します。

```sh
/plugin install traceary
```

`/plugin install` のスコープ指定オプションは、現在の Claude Code slash-command 経由 install では使用できません。Claude Code の plugin 設定 UI から有効化スコープを管理してください。

## Update

```sh
/plugin marketplace update traceary-plugins
/plugin update traceary
```

> **重要**: `brew upgrade traceary` は CLI binary を更新しますが、**Claude plugin cache には触れません**。新しい Traceary リリースが hook を追加したとき（例: v0.8 の transcript hook や built-in tool matcher hook）は、Claude Code セッション内で新 hook を有効化するために `/plugin update traceary` も実行する必要があります。`traceary doctor --client claude` は cache が marketplace manifest より古い状態を `claude-plugin-cache` check で警告します。

## Uninstall

```sh
/plugin uninstall traceary
```

marketplace 自体も不要なら、次で外せます。

```sh
/plugin marketplace remove traceary-plugins
```

## plugin 導入時は `hooks install` は不要

`traceary hooks install --client claude` は settings.json に Traceary hooks を書き込みますが、plugin をインストールしている場合は Claude Code にすでに同じ hook が提供されています。このため **plugin が有効な状態で `hooks install` を実行すると audit が 1 回のツール呼び出しにつき 2 回記録されます**。

- `traceary hooks install --client claude` は有効な plugin を検出し（`~/.claude/settings.json` の `enabledPlugins` を参照）、メッセージを出してスキップします。開発目的などで両方に登録したい場合は `--force` を使用してください
- `traceary doctor --client claude` は、plugin が有効でかつ settings.json にも Traceary 管理下の hook がある場合は `warn` を返します

## Doctor と smoke test

実運用の確認は次を基本にします。

```sh
traceary doctor --client claude --json
```

package 自体の validate は次です。

```sh
claude plugins validate .claude-plugin/marketplace.json
claude plugins validate integrations/claude-plugin
```

この repository からの end-to-end smoke test は次です。

```sh
./scripts/smoke_test_integrations.sh claude
```
