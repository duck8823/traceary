# Agent SDK integration evaluation

[日本語](./agent-sdks.ja.md)

Part of #567 · closes #571 evaluation.

This document answers "how do I use Traceary's memory store from my agent SDK?" for the major 2026 SDKs, decides whether Traceary ships a native adapter for each, and records the reasoning.

## Summary matrix

| SDK | MCP support | Native memory abstraction | Traceary-custom adapter needed? | v0.9 decision |
|---|---|---|---|---|
| Claude Agent SDK | ✅ stable (`mcp_servers`) | `BetaAbstractMemoryTool` (Python / TypeScript) | **Split** — MCP integration now (this release); native memory-tool backend deferred (#699) | docs now, native backend defer |
| OpenAI Agents SDK | ✅ stable (`MCPServerStdio` / `MCPServerSse`) | `Session` persistence is backend-swappable; no memory-tool abstraction | No | defer (MCP is enough) |
| Google ADK | ✅ stable (`mcp_tool`) | `MemoryService` pluggable backend | No (yet) — revisit when ADK memory API stabilises | defer (MCP is enough) |

## Claude Agent SDK

**Status**: connect via MCP today; native memory-tool backend deferred.

`traceary mcp-server` exposes `retrieve_memories`, `remember_memory`, `accept_memory`, and the graph overlay via the standard MCP protocol. The Claude Agent SDK consumes MCP servers through the `mcp_servers` config — wiring Traceary is one block:

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

That covers the **external-tool** path: the agent calls `traceary.retrieve_memories(...)` explicitly, and Traceary owns the store.

`BetaAbstractMemoryTool` is a different surface — it lets the SDK redirect the model's **built-in `memory` tool** (which the model may invoke without explicit prompting) to a custom backend. Building that adapter would legitimately route more traffic into Traceary, but it means maintaining a Python package that tracks Anthropic's beta API. v0.9 defers it; #699 will revisit once Anthropic ships a stable memory-tool abstraction and the signal from operators is clearly in favour.

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

ADK supports MCP tools via `mcp_tool`. Usage pattern matches the others:

```python
from google.adk.tools import mcp_tool
from google.adk.agents import Agent

memory_tools = mcp_tool.tools_from_mcp_stdio(command="traceary", args=["mcp-server"])
agent = Agent(tools=memory_tools)
```

ADK also has a `MemoryService` pluggable backend that could theoretically hold Traceary data. That surface is younger than Anthropic's memory-tool abstraction and — per current docs — still shifting. Shipping a Traceary-custom `MemoryService` today risks chasing a moving API. Defer to post-v0.9 and reassess once the ADK memory API stabilises.

## What we explicitly did NOT do in v0.9

- No Python packages added under `integrations/`. Traceary stays Go-only from a distributed-artifact perspective. `scripts/*.py` are release-tooling helpers and are not user-facing.
- No `BetaAbstractMemoryTool` subclass (tracked in #699 as a follow-up).
- No `MemoryService` adapter for Google ADK.

## Follow-up issues

- #699 — experiment with Anthropic native memory-tool backend for Traceary (opened when this doc ships).

## Revisit cadence

Revisit this doc at the v0.10 / v1.0 planning gate. SDK APIs (especially Anthropic's memory tool, still in beta) will move, and the right call now — "MCP is enough" — is specifically time-bound.
