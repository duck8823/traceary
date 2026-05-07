# Claude Code plugin

[English](./claude-plugin.md)

Claude 向け package は `integrations/claude-plugin/` にあり、repository root の `.claude-plugin/marketplace.json` から配布します。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart` / `SessionEnd` hook
- `Bash` / `mcp__.*` / 組み込み tool matcher (`Read`, `NotebookRead`, `Edit`, `MultiEdit`, `Write`, `NotebookEdit`, `Grep`, `Glob`, `Agent`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `ExitPlanMode`) 向けの `PostToolUse` / `PostToolUseFailure` audit hook
- slash command として使える `/traceary-help` と、文脈で自動適用される `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember` skill。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。

## Memory activation strategy

Claude integration は、Traceary の accepted memory store を MCP tools / instruction-file export / host-native activation 経由で使います。review 済み memory を Claude project instructions に見せる方法は二つあります。

**Option 1 — instruction-file export（従来通り利用可）。** Traceary 管理ブロックを `CLAUDE.md` に直接 export します。

```sh
traceary memory admin export --target claude --out CLAUDE.md
```

**Option 2 — host-native activation（v0.13.0+ で利用可、project では推奨）。** `traceary memory admin activate --target claude` で `CLAUDE.md` 内の小さな import stub と `.traceary/memories/claude.md` の external memory file を二ファイル構成で管理します。activation pair は管理ブロック外の user-authored content を保持し、安全でない対象（symlink / directory / 不正マーカー / 新バージョンマーカー）を拒否し、冪等です。Traceary は Claude が所有する `~/.claude/projects/<project>/memory/` 配下の auto memory に書き込みません。その store は Claude 自身の所有のままです。

```sh
# host pair を read-only で点検
traceary memory admin activate --target claude --status

# 適用予定の差分を事前確認 (dry-run, 書き込みなし)
traceary memory admin activate --target claude --dry-run --diff

# pair を file 単位の safe write で反映（idempotent）
traceary memory admin activate --target claude --apply
```

既定値:

- activation root: 直近の `.git` 祖先（無ければ作業ディレクトリ）
- host context file: `<root>/CLAUDE.md`
- external memory file: `<root>/.traceary/memories/claude.md`
- `CLAUDE.md` に書き込まれる import 行: `@./.traceary/memories/claude.md`

`--root <dir>` / `--path <file>` で override 可能です。詳細な contract（managed marker layout、status の状態、tracked-file ポリシー）は v0.13 host-native memory activation [ADR](../architecture/host-native-memory-activation.ja.md) を参照してください。`invalid` からの復旧手順は [durable memory ガイド](../memory/README.ja.md#invalid-からの復旧)にまとめています。`traceary doctor --client claude` は `claude-memory-activation` チェックで同じ dry-run / apply 再実行コマンドを案内します。

Anthropic SDK loop を自前で持つ場合は experimental な [Anthropic native memory tool](./anthropic-memory-tool.ja.md) backend も使えますが、その store は curated な `memories` aggregate とは分離しています。

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
