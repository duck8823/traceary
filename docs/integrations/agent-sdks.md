# Agent SDK integration evaluation

[日本語](./agent-sdks.ja.md)

Part of #567 · closes #571 evaluation. Updated for v0.35.0 (#1871).

This document answers "how do I use Traceary's memory store from my agent SDK?" for the major 2026 SDKs, decides whether Traceary ships a native adapter for each, and records the reasoning.

## Current product surface

As of **v0.35.0 (#1871)** Traceary **does not ship an MCP server**. `traceary mcp-server` is an unknown command. Shipped host packages no longer declare `mcpServers`.

**Use today:**

| Job | Path |
|---|---|
| Host capture | packaged hooks (`traceary hook …`) |
| Agent read / write guidance | shipped skills (CLI commands) |
| Direct operator / script use | Traceary CLI (`memory …`, `session …`, `context`, `search`, `list`, `report`, …) |
| Direct Anthropic Go SDK loop | experimental `pkg/anthropicmemory` ([native memory tool](./anthropic-memory-tool.md)) |

Do **not** spawn `traceary mcp-server` from new code or host config.

## Summary matrix

| SDK | Integration today | Traceary-custom adapter needed? | Current decision |
|---|---|---|---|
| Claude / Anthropic APIs | Packaged Claude plugin (hooks + skills) and/or CLI; optional experimental Go native `memory_20250818` backend | **No MCP.** Native Go backend only when you own the Anthropic loop | [Claude plugin](./claude-plugin.md) + CLI/skills; [native memory tool](./anthropic-memory-tool.md) when applicable |
| OpenAI Agents SDK | Shell out to the Traceary CLI from tools/skills; no Traceary MCP server | No custom adapter | defer custom adapter; use CLI |
| Google ADK | Shell out to the Traceary CLI from tools; no Traceary MCP server | No custom adapter (revisit ADK `MemoryService` later) | defer custom adapter; use CLI |

## Claude / Anthropic APIs

**Status**: use the packaged Claude integration (hooks + skills) and the CLI. For a direct Anthropic API loop you own in Go, the experimental native memory-tool backend is available.

Agent hosts that previously registered Traceary as an MCP server must remove that registration and use skills / CLI instead. Example CLI surfaces skills already document:

- Discovery / history: `traceary list`, `traceary search`, `traceary show`
- Resume pack: `traceary session handoff`, `traceary context`
- Durable memory: `traceary memory store …`, `memory inbox …`, `memory search`

Anthropic's native `memory_20250818` tool is a different surface: the model emits memory commands and the client application executes them. Traceary ships an experimental Go backend for that flow in `pkg/anthropicmemory`; see [Anthropic native memory tool](./anthropic-memory-tool.md). That path is useful only when you own the Anthropic Go SDK loop directly — not as a substitute MCP server.

## OpenAI Agents SDK

**Status**: call the Traceary CLI from host tools or scripts; no Traceary MCP server; defer a custom adapter.

The Agents SDK can still host *other* MCP servers via `MCPServerStdio` / `MCPServerSse`. That is unrelated to Traceary. For Traceary data, invoke `traceary` on the shell (same commands as the shipped skills).

OpenAI's SDK has no explicit long-term memory abstraction equivalent to Anthropic's `memory_20250818` client tool — `Session` is for conversation state, not a pluggable durable-memory backend. A Traceary-custom adapter is not justified while the CLI covers the same jobs.

## Google ADK

**Status**: call the Traceary CLI from tools; no Traceary MCP server; Traceary-native `MemoryService` adapter deferred.

ADK can attach third-party MCP toolsets, but Traceary no longer provides one. Prefer shell/CLI invocation for list/search/memory/session work.

ADK also has a `MemoryService` pluggable backend that could theoretically hold Traceary data. That surface is younger than Anthropic's memory-tool abstraction and still shifting. Shipping a Traceary-custom `MemoryService` today risks chasing a moving API. Defer and reassess once the ADK memory API stabilises.

## Historical note (retired MCP path)

Before v0.35.0, some SDKs were wired with `command: "traceary", args: ["mcp-server"]`. That command and every shipped MCP declaration were removed in #1871. Do not copy old snippets that start `traceary mcp-server`; they fail as an unknown command.

## What remains out of scope

- No Python / TypeScript convenience wrappers for Anthropic memory-tool backends; the Go API is the experimental native surface.
- No `MemoryService` adapter for Google ADK.
- No reintroduction of a Traceary MCP server.

## Revisit cadence

Revisit this doc at the v1.0 planning gate. SDK APIs (especially Anthropic's memory tool) will move. The right call after #1871 is: **CLI + skills by default**, experimental native memory tool only when you own the Anthropic loop.
