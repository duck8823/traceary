# Agent SDK 統合の評価

[English](./agent-sdks.md)

#567 の一部 · #571 の評価フェーズを閉じる。

このドキュメントは「自分の agent SDK から Traceary の memory ストアをどう使うか」を、2026 年時点の主要 SDK について整理し、Traceary-native adapter を出荷するか判断と、その根拠を記録するものです。

## サマリーマトリクス

| SDK | MCP 対応 | native memory 抽象 | Traceary 独自 adapter 必要？ | v0.9 判定 |
|---|---|---|---|---|
| Claude Agent SDK | ✅ 安定 (`mcp_servers`) | `BetaAbstractMemoryTool` (Python / TypeScript) | **分割** — MCP 統合は今リリース、native memory-tool backend は defer (#699) | 今は docs、native backend は defer |
| OpenAI Agents SDK | ✅ 安定 (`MCPServerStdio` / `MCPServerSse`) | `Session` は backend 差し替え可能、memory-tool 抽象はなし | 不要 | defer (MCP で十分) |
| Google ADK | ✅ 安定 (`mcp_tool`) | `MemoryService` pluggable backend | 不要 (今は) — ADK memory API が安定したら再評価 | defer (MCP で十分) |

## Claude Agent SDK

**現状**: MCP 経由で今日から使える。native memory-tool backend は defer。

`traceary mcp-server` は `retrieve_memories` / `remember_memory` / `accept_memory` と graph overlay を標準 MCP で公開しています。Claude Agent SDK は `mcp_servers` 設定で MCP server を取り込むので、Traceary の配線はブロック 1 個です:

```python
from claude_agent_sdk import ClaudeAgentClient

client = ClaudeAgentClient(
    mcp_servers={
        "traceary": {
            "command": "traceary",
            "args": ["mcp-server"],
        },
    },
)
```

ここまでが **外部 tool 呼び出し**の経路。agent が `traceary.retrieve_memories(...)` を明示的に叩き、Traceary がストアを持つ形です。

`BetaAbstractMemoryTool` はこれと別の面です。SDK が持つ **built-in `memory` tool** (モデルが明示指示なしに使うことがある) を独自 backend に差し替える抽象です。これを作れば Traceary への流量は正当に増えますが、Anthropic の beta API に追従する Python パッケージを維持する義務が発生します。v0.9 では defer。#699 で、Anthropic の memory-tool 抽象が安定し、運用者からの明確な要望が出た段階で再評価します。

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

ADK は `mcp_tool` で MCP を取り込みます。使い方は他の SDK と同形です:

```python
from google.adk.tools import mcp_tool
from google.adk.agents import Agent

memory_tools = mcp_tool.tools_from_mcp_stdio(command="traceary", args=["mcp-server"])
agent = Agent(tools=memory_tools)
```

ADK には `MemoryService` という pluggable backend もあり、理論上は Traceary 用に実装できます。ただこの面は Anthropic の memory-tool 抽象より若く、公開 docs の範囲で API が動いている印象です。今作ると動く target を追いかけ続けるので、v0.9 では defer。ADK memory API が安定したら再評価します。

## v0.9 で明示的に「しないこと」

- `integrations/` 配下に Python パッケージを足さない。Traceary は配布物レベルで Go-only を維持。`scripts/*.py` はリリース検証ヘルパーでユーザーには届きません。
- `BetaAbstractMemoryTool` の subclass は書かない (follow-up として #699 で tracking)。
- Google ADK の `MemoryService` adapter は書かない。

## Follow-up issue

- #699 — Anthropic native memory-tool backend を Traceary で試す (本 doc 出荷時に open)。

## 再評価タイミング

v0.10 / v1.0 プランニングゲートでこの doc を再評価します。SDK API (特に Anthropic の memory tool、まだ beta) は動き続けるので、今の判断「MCP で十分」は明示的に時限付きです。
