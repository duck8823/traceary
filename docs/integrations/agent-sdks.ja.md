# Agent SDK 統合の評価

[English](./agent-sdks.md)

#567 の一部 · #571 の評価フェーズを閉じる。

このドキュメントは「自分の agent SDK から Traceary の memory ストアをどう使うか」を、2026 年時点の主要 SDK について整理し、Traceary-native adapter を出荷するか判断と、その根拠を記録するものです。

## サマリーマトリクス

| SDK | MCP 対応 | native memory 抽象 | Traceary 独自 adapter 必要？ | 現在の判定 |
|---|---|---|---|---|
| Claude / Anthropic APIs | ✅ agent host では安定 (`mcp_servers`) | Anthropic SDK の native `memory_20250818` client tool | **両方** — MCP は portable path、Go native backend は experimental | MCP docs + [native memory tool](./anthropic-memory-tool.ja.md) |
| OpenAI Agents SDK | ✅ 安定 (`MCPServerStdio` / `MCPServerSse`) | `Session` は backend 差し替え可能、memory-tool 抽象はなし | 不要 | defer (MCP で十分) |
| Google ADK | ✅ 安定 (`McpToolset` + `StdioConnectionParams`) | `MemoryService` pluggable backend | 不要 (今は) — ADK memory API が安定したら再評価 | defer (MCP で十分) |

## Claude / Anthropic APIs

**現状**: agent host は MCP 経由で使う。Anthropic API を直接組み込む場合は experimental な Go native memory-tool backend を使える。

`traceary mcp-server` は `query_memory(action="retrieve")` / `manage_memory(action="remember")` / `manage_memory(action="accept")` 等の memory tool を標準 MCP で公開しています (v0.9 の graph overlay は `traceary memory graph` の CLI 側のみ。MCP tool は follow-up)。Claude Agent SDK は `ClaudeAgentOptions.mcp_servers` 経由で取り込みます:

```python
from claude_agent_sdk import query, ClaudeAgentOptions

options = ClaudeAgentOptions(
    mcp_servers={
        "traceary": {
            "command": "traceary",
            "args": ["mcp-server"],
        },
    },
)

async for message in query(prompt="...", options=options):
    ...
```

streaming の場合は `ClaudeSDKClient` に同じ `options` を渡す形になります。

ここまでが **外部 tool 呼び出し**の経路。agent が `traceary.query_memory(action="retrieve")(...)` を明示的に叩き、Traceary がストアを持つ形です。

Anthropic の native `memory_20250818` tool は別の面です。model が memory command を出し、client application がそれを実行します。Traceary はこの flow 用の experimental Go backend を `pkg/anthropicmemory` として提供します。詳細は [Anthropic native memory tool](./anthropic-memory-tool.ja.md) を参照してください。Anthropic Go SDK の loop を直接持つ場合に有用です。hosted agent SDK や multi-provider workflow では MCP が引き続き default path です。

## OpenAI Agents SDK

**現状**: MCP が sanctioned path。Traceary 独自 adapter は defer。

SDK は `MCPServerStdio` / `MCPServerSse` を標準提供しており、複数 transport を docs 化しています。Traceary の `mcp-server` stdio をそのまま接続できます:

```python
from agents import Agent
from agents.mcp import MCPServerStdio

async with MCPServerStdio(params={"command": "traceary", "args": ["mcp-server"]}) as traceary:
    agent = Agent(name="session", mcp_servers=[traceary])
```

OpenAI SDK には `BetaAbstractMemoryTool` に対応する memory 抽象はありません。`Session` は会話状態の永続化用で、長期 memory の pluggable backend ではない。MCP で出来ることを超える Traceary 独自 adapter の価値はないので defer。

## Google ADK

**現状**: MCP 統合が今日から動く。Traceary-native `MemoryService` adapter は defer。

ADK は `McpToolset` + `StdioConnectionParams` で MCP を取り込みます。使い方は他の SDK と同形です:

```python
from google.adk.agents import Agent
from google.adk.tools.mcp_tool import McpToolset, StdioConnectionParams
from mcp import StdioServerParameters

traceary = McpToolset(
    connection_params=StdioConnectionParams(
        server_params=StdioServerParameters(command="traceary", args=["mcp-server"]),
    ),
)
agent = Agent(tools=[traceary])
```

ADK には `MemoryService` という pluggable backend もあり、理論上は Traceary 用に実装できます。ただこの面は Anthropic の memory-tool 抽象より若く、公開 docs の範囲で API が動いている印象です。今作ると動く target を追いかけ続けるので、v0.9 では defer。ADK memory API が安定したら再評価します。

## 引き続き scope 外のもの

- Anthropic memory-tool backend の Python / TypeScript convenience wrapper は出さない。experimental native surface は Go API。
- Google ADK の `MemoryService` adapter は書かない。

## 再評価タイミング

v1.0 プランニングゲートでこの doc を再評価します。SDK API (特に Anthropic の memory tool、まだ beta) は動き続けるので、今の判断「default は MCP、Anthropic loop を直接持つ場合は native memory tool」は明示的に時限付きです。
