# Gemini CLI extension

[English](./gemini-extension.md)

Gemini 向け package は `integrations/gemini-extension/` にあります。Gemini CLI は install された extension の root に `gemini-extension.json` があることを前提にするため、Traceary では tagged release ごとにこの package を専用 archive として配布します。

## 自動で組み込むもの

- `traceary mcp-server` を使う `traceary` MCP server
- `SessionStart` / `SessionEnd` hook
- `AfterAgent` transcript hook（agent の応答を `transcript` event として記録）
- `run_shell_command` 向け `AfterTool` audit hook
- slash command の `/traceary-help` と `/traceary-doctor`
- 文脈で効く `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember` skill。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。旧 `traceary-memory-capture` は deprecated stub として残存（v0.12 で削除予定）。

## Memory activation strategy

Gemini integration の v0.12 は、Traceary の accepted memory store を MCP tools と instruction-file export 経由で使います。review 済み memory を Gemini instructions に見せるには、Traceary 管理ブロックとして export します。

```sh
traceary memory export --target gemini --out GEMINI.md
```

これは Codex host-native activation とは意図的に分けています。`traceary memory activate --target gemini` は v0.12 では**未実装**で、extension は Gemini-native memory file を書きません。host-native surface と preview / feature-flag 挙動が十分安定してから扱うため、将来の安全な Gemini-native activation path は follow-up #884 で追跡します。

## Install

1. 先に Traceary CLI を入れます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Traceary の GitHub release から extension を install します。

```sh
gemini extensions install https://github.com/duck8823/traceary --ref <tag>
```

Traceary では、archive root が Gemini CLI の期待する extension root になる `traceary.tar.gz` asset を release ごとに公開します。

この repository を使ってローカル開発したい場合は、代わりに link を使います。

```sh
gemini extensions link integrations/gemini-extension
```

## Update

```sh
gemini extensions update traceary
```

特定の release に戻したい場合は、`--ref <tag>` を付けて再 install します。

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor と smoke test

実運用の確認は次を基本にします。

```sh
traceary doctor --client gemini --json
```

package 自体の validate は次です。

```sh
gemini extensions validate integrations/gemini-extension
```

この repository からの end-to-end smoke test は次です。

```sh
./scripts/smoke_test_integrations.sh gemini
```
