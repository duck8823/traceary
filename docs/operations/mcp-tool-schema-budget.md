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
runtime response. It has two hand-edited aggregate warning bands and hard
limits:

| Measurement | Pass range | Warning band | Hard limit |
| --- | ---: | ---: | ---: |
| Complete `tools/list` result | below 48 KiB | 48–52 KiB (49,152–53,248 B) | 52 KiB (53,248 B) |
| Sum of input schemas | below 16 KiB | 16–18 KiB (16,384–18,432 B) | 18 KiB (18,432 B) |

The test also has a reviewed, per-tool hard cap. The checked-in report
`presentation/mcpserver/testdata/tool_schema_budget.golden.json` identifies
the source of growth. Per-tool warning thresholds remain 90% of their
hand-edited hard caps. A value in a warning band is logged; a value above its
hard limit fails CI. Limits are policy, not generated output: change them only
with a justification and a reviewed report.

The complete-response band is repository policy, not a claimed host limit. It
was calibrated so both the initial 41,028 B response and the already reviewed
pagination-schema expansion remain below the warning band, while the final
4 KiB before the hard limit remains an explicit intervention window.

Run the focused report locally:

```sh
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -v
```

Intentional contract changes require both fixtures to be regenerated together
from the final integrated commit and reviewed:

```sh
go test ./presentation/mcpserver \
  -run 'TestServer_Tool(RegistrySnapshot|AdvertisementBudget)$' \
  -update -update-tool-schema-budget
```

Do not regenerate one fixture on an older branch and copy it across a parallel
schema change. Rebase or integrate every intended schema-producing change
first, run the combined command once, and review both diffs from that same
tree. The update flags record observed schemas and bytes only; they never
change the hand-edited warning bands or hard limits.

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
