# Agent SDK integration evaluation

[日本語](./agent-sdks.ja.md)

Part of #567 · closes #571 evaluation.

This document answers "how do I use Traceary's memory store from my agent SDK?" for the major 2026 SDKs, decides whether Traceary ships a native adapter for each, and records the reasoning.

## Summary matrix

| SDK | MCP support | Native memory abstraction | Traceary-custom adapter needed? | Current decision |
|---|---|---|---|---|
| Claude / Anthropic APIs | ✅ stable (`mcp_servers`) for agent hosts | Native `memory_20250818` client tool in the Anthropic SDKs | **Both** — MCP remains the portable path; Go native backend is experimental | MCP docs + [native memory tool](./anthropic-memory-tool.md) |
| OpenAI Agents SDK | ✅ stable (`MCPServerStdio` / `MCPServerSse`) | `Session` persistence is backend-swappable; no memory-tool abstraction | No | defer (MCP is enough) |
| Google ADK | ✅ stable (`McpToolset` + `StdioConnectionParams`) | `MemoryService` pluggable backend | No (yet) — revisit when ADK memory API stabilises | defer (MCP is enough) |

## Claude / Anthropic APIs

**Status**: connect via MCP for agent hosts; use the experimental Go native memory-tool backend for direct Anthropic API integrations.

`traceary mcp-server` exposes `query_memory(action="retrieve")`, `manage_memory(action="remember")`, `manage_memory(action="accept")`, and related memory tools via the standard MCP protocol. (The v0.9 graph overlay is CLI-only via `traceary memory graph`; MCP graph tools are a follow-up.) The Claude Agent SDK consumes MCP servers through `ClaudeAgentOptions.mcp_servers`:

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

`ClaudeSDKClient` (streaming variant) accepts the same `options` — pick whichever matches your invocation style.

That covers the **external-tool** path: the agent calls `traceary.query_memory(action="retrieve")(...)` explicitly, and Traceary owns the store.

Anthropic's native `memory_20250818` tool is a different surface: the model emits memory commands and the client application executes them. Traceary now ships an experimental Go backend for that flow in `pkg/anthropicmemory`; see [Anthropic native memory tool](./anthropic-memory-tool.md). This is useful when you own the Anthropic Go SDK loop directly. For hosted agent SDKs and multi-provider workflows, MCP remains the default integration path.

## OpenAI Agents SDK

**Status**: MCP is the sanctioned path; defer a Traceary-custom adapter.

The SDK exposes `MCPServerStdio` / `MCPServerSse` out of the box and documents multiple transports. Traceary's `mcp-server` stdio works unchanged:

```python
from agents import Agent
from agents.mcp import MCPServerStdio

async with MCPServerStdio(params={"command": "traceary", "args": ["mcp-server"]}) as traceary:
    agent = Agent(name="session", mcp_servers=[traceary])
```

OpenAI's SDK has no explicit memory abstraction equivalent to `BetaAbstractMemoryTool` — `Session` is for conversation state persistence, not a pluggable long-term memory backend. There is nothing a Traceary-custom adapter would provide that MCP does not already provide; defer.

## Google ADK

**Status**: MCP integration works today; Traceary-native `MemoryService` adapter deferred.

ADK supports MCP tools via `McpToolset` + `StdioConnectionParams`. Usage pattern matches the others:

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

ADK also has a `MemoryService` pluggable backend that could theoretically hold Traceary data. That surface is younger than Anthropic's memory-tool abstraction and — per current docs — still shifting. Shipping a Traceary-custom `MemoryService` today risks chasing a moving API. Defer to post-v0.9 and reassess once the ADK memory API stabilises.

## What remains out of scope

- No Python / TypeScript convenience wrappers for Anthropic memory-tool backends; the Go API is the experimental native surface.
- No `MemoryService` adapter for Google ADK.

## Revisit cadence

Revisit this doc at the v1.0 planning gate. SDK APIs (especially Anthropic's memory tool, still in beta) will move, and the right call now — "MCP by default, native memory tool when you own the Anthropic loop" — is specifically time-bound.
