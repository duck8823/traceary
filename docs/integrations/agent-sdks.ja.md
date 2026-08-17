# Agent SDK 統合の評価

[English](./agent-sdks.md)

#567 の一部 · #571 evaluation を close。v0.35.0 (#1871) 向けに更新。

このドキュメントは、主要な 2026 SDK について「agent SDK から Traceary の memory store をどう使うか」を答え、Traceary が各 SDK 向けに native adapter を出すかを決め、その理由を残します。

## 現在の製品面

**v0.35.0 (#1871)** 以降、Traceary は **MCP server を出荷しません**。`traceary mcp-server` は unknown command です。出荷 host package も `mcpServers` を宣言しません。

**今日使う経路:**

| 用途 | 経路 |
|---|---|
| host の自動記録 | 同梱 hooks（`traceary hook …`） |
| agent の read / write 案内 | 同梱 skills（CLI コマンド） |
| 運用者 / スクリプト | Traceary CLI（`memory …`、`session …`、`context`、`search`、`list`、`report` など） |
| Anthropic Go SDK を直接回す場合 | experimental な `pkg/anthropicmemory`（[native memory tool](./anthropic-memory-tool.ja.md)） |

新しいコードや host config から **`traceary mcp-server` を起動しないでください**。

## サマリ matrix

| SDK | 今日の統合 | Traceary 独自 adapter が必要か | 現在の判定 |
|---|---|---|---|
| Claude / Anthropic APIs | 同梱 Claude plugin（hooks + skills）および/または CLI。任意で experimental な Go native `memory_20250818` backend | **MCP なし。** Anthropic loop を自前で持つときだけ native Go backend | [Claude plugin](./claude-plugin.ja.md) + CLI/skills。該当時は [native memory tool](./anthropic-memory-tool.ja.md) |
| OpenAI Agents SDK | tools / skills から Traceary CLI を呼ぶ。Traceary MCP server なし | 独自 adapter なし | custom adapter は defer。CLI を使う |
| Google ADK | tools から Traceary CLI を呼ぶ。Traceary MCP server なし | 独自 adapter なし（後で ADK `MemoryService` を再評価） | custom adapter は defer。CLI を使う |

## Claude / Anthropic APIs

**現状**: 同梱 Claude 統合（hooks + skills）と CLI を使う。Go で Anthropic API loop を自前で持つ場合のみ、experimental な native memory-tool backend を使える。

以前 Traceary を MCP server として登録していた agent host は、その登録を外し skills / CLI に切り替えてください。skill が案内する CLI の例:

- Discovery / history: `traceary list`、`traceary search`、`traceary show`
- Resume pack: `traceary context --handoff`、`traceary context`
- Durable memory: `traceary memory store …`、`memory inbox …`、`memory search`

Anthropic の native `memory_20250818` tool は別の面です。model が memory command を出し、client application がそれを実行します。Traceary はこの flow 用の experimental Go backend を `pkg/anthropicmemory` として提供します。詳細は [Anthropic native memory tool](./anthropic-memory-tool.ja.md) を参照してください。Anthropic Go SDK の loop を直接持つ場合に有用で、MCP server の代替ではありません。

## OpenAI Agents SDK

**現状**: host tools や scripts から Traceary CLI を呼ぶ。Traceary MCP server なし。custom adapter は defer。

Agents SDK は引き続き *他の* MCP server を `MCPServerStdio` / `MCPServerSse` で載せられます。それは Traceary とは無関係です。Traceary のデータには shell 上の `traceary`（同梱 skill と同じコマンド）を使います。

OpenAI SDK には Anthropic の `memory_20250818` client tool に相当する長期 memory 抽象はありません。`Session` は会話状態の永続化用で、pluggable な durable memory backend ではありません。CLI が同じ仕事をカバーする間、Traceary 独自 adapter の根拠はありません。

## Google ADK

**現状**: tools から Traceary CLI を呼ぶ。Traceary MCP server なし。Traceary-native `MemoryService` adapter は defer。

ADK は第三者の MCP toolset を付けられますが、Traceary はもうそれを提供しません。list / search / memory / session 作業は shell / CLI 呼び出しを優先してください。

ADK には Traceary データを載せられる可能性のある pluggable `MemoryService` もあります。この面は Anthropic の memory-tool 抽象より新しく、まだ動いています。今 `MemoryService` を出すと API 追従コストが高いので defer し、ADK memory API が安定したら再評価します。

## 履歴メモ（退役した MCP 経路）

v0.35.0 より前は、一部 SDK を `command: "traceary", args: ["mcp-server"]` で配線していました。そのコマンドと出荷していた MCP 宣言は #1871 で削除されました。`traceary mcp-server` を起動する旧 snippet をコピーしないでください。unknown command として失敗します。

## スコープ外

- Anthropic memory-tool backend 向けの Python / TypeScript convenience wrapper は出さない（experimental native surface は Go API）。
- Google ADK 向け `MemoryService` adapter は出さない。
- Traceary MCP server の再導入はしない。

## 再評価タイミング

v1.0 プランニングゲートでこの doc を再評価します。SDK API（特に Anthropic の memory tool）は動き続けます。#1871 以降の正しい既定は **CLI + skills**、Anthropic loop を自前で持つときだけ experimental native memory tool です。
