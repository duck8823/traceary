# MCP server (retired)

[日本語](./README.ja.md)

The Traceary MCP server (`traceary mcp-server`) and its tools were **removed in v0.35.0** (#1871).

Invoking `traceary mcp-server` fails as an unknown command with a non-zero exit and no `DEPRECATED` notice. Shipped host packages no longer declare an MCP server.

Use the CLI for the same work:

| Former MCP surface | CLI replacement |
|---|---|
| session / handoff context | `traceary session handoff`, `context`, `list`, `search` |
| search / list events / report | `traceary search`, `list`, `report` |
| memory manage / query | `traceary memory store …`, `memory inbox …`, `memory admin …`, `memory search` |

Hook capture was always shell-based (`traceary hook …`) and is unchanged. Claude `hooks.json` keeps `matcher: mcp__.*` so audits of *other* servers continue.

Policy note: removal is an explicit exception to the one-minor deprecation window — see the historical removal log in [CLI stability](../cli-stability.md).
