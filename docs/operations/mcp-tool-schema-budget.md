# MCP tool schema budget

[日本語](./mcp-tool-schema-budget.ja.md)

Traceary advertises one default MCP surface: all nine tools are registered by
`traceary mcp-server`. It does not select tool profiles, split the registry, or
make a subset conditional on the host.

## Guarded contract

`TestServer_ToolRegistrySnapshot` calls runtime `tools/list` through an
in-memory MCP transport. Its reviewed fixture includes each tool's name,
description, annotations, input schema, and inferred output schema.

`TestServer_ToolAdvertisementBudget` measures compact JSON from that same
runtime response. It has two hand-edited aggregate hard limits:

| Measurement | Hard limit | Warning threshold |
| --- | ---: | ---: |
| Complete `tools/list` result | 45,056 B | 90% (40,550 B) |
| Sum of input schemas | 18,432 B | 90% (16,589 B) |

The test also has a reviewed, per-tool hard cap. The checked-in report
`presentation/mcpserver/testdata/tool_schema_budget.golden.json` identifies
the source of growth. A value at or above 90% is logged as a warning; a value
above its hard limit fails CI. Limits are policy, not generated output: change
them only with a justification and a reviewed report.

Run the focused report locally:

```sh
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -v
```

Intentional contract changes require both fixtures to be regenerated and
reviewed:

```sh
go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot -update
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -update-tool-schema-budget
```

## Host command registration

The Claude, Codex, Gemini, Antigravity, Grok, and Kimi packages all register
the same command: `traceary mcp-server`. `go run ./cmd/repo-tooling
integrations verify` rejects an alternate executable, an omitted argument, or
an added profile argument. A host-native envelope field such as Gemini's
working directory does not select a different tool surface.

## Body-free loading observation

This repository can verify the server's complete runtime advertisement and the
package registration without reading event or transcript bodies. It cannot
measure when a third-party host loads schemas after installation. The state
labels below are deliberately distinct:

| State | Meaning | Current evidence |
| --- | --- | --- |
| eager | The host is observed loading the complete registry during setup | No body-free host observation recorded |
| lazy | The host is observed deferring registry loading until use | No body-free host observation recorded |
| unknown | The package registers the command, but loading timing is not observed | Claude, Codex, Gemini, Antigravity, Grok, Kimi |

`unknown` is not evidence for lazy loading. Do not relabel a host eager or lazy
until a reproducible, body-free host observation is recorded.
