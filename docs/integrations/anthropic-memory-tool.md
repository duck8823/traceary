# Anthropic native memory tool (experimental)

[日本語](./anthropic-memory-tool.ja.md)

Traceary can back Anthropic's native beta memory tool (`memory_20250818`) from Go code. The handler lives in `pkg/anthropicmemory` and stores the tool's filesystem-shaped state in the local SQLite `memory_tool_files` table.

This integration is **experimental** for v0.10. The public Go API may change while Anthropic's memory tool remains beta.

## Native memory tool vs Traceary MCP memory tools

Use the native Anthropic memory tool when:

- you are already calling Anthropic's Messages API / Go SDK directly;
- you want Claude to decide when to `view`, `create`, `str_replace`, `insert`, `delete`, or `rename` memory files;
- filesystem-shaped, model-managed memory under `/memories/...` is acceptable.

Use Traceary's MCP memory tools (`traceary mcp-server`, `manage_memory`, `query_memory`) when:

- the agent host already supports MCP (Claude Code, Codex, Gemini CLI, OpenAI Agents SDK, Google ADK, etc.);
- you want operator-curated durable memories with status, confidence, evidence, validity windows, and review/acceptance workflows;
- you need the same interface across multiple model providers.

The two stores are intentionally separate. `memory_tool_files` is the native memory-tool file backend; `memories` is Traceary's durable knowledge aggregate.

## Threat model and limits

- **Path traversal**: the v0.10-50 handler validates every path before storage. Paths must be canonical `/memories/...` paths; relative paths, `..`, backslash traversal, URL-encoded traversal, and absolute paths outside `/memories` are rejected before reaching SQLite.
- **Size limits**: the memory-tool backend stores content as SQLite `BLOB`s. Traceary currently enforces the command-level line limit used by the renderer (999,999 lines) and caches `size_bytes` for listings. Operators should still treat this as local untrusted model output: monitor DB growth, use normal SQLite backups, and avoid exposing the handler to arbitrary non-Anthropic inputs.
- **Client-side execution**: Anthropic's memory tool is a client tool. The model emits a command payload; your application executes it and sends a `tool_result` back.

## Go SDK wiring

Install / update the SDK:

```sh
go get github.com/anthropics/anthropic-sdk-go@v1.38.0
```

Register the pinned memory tool with the beta Messages API:

```go
import (
    "context"
    "io/fs"
    "os"

    "github.com/anthropics/anthropic-sdk-go"
    "github.com/anthropics/anthropic-sdk-go/option"

    "github.com/duck8823/traceary/infrastructure/sqlite"
    "github.com/duck8823/traceary/pkg/anthropicmemory"
)

func buildHandler(dbPath string) (*anthropicmemory.Handler, error) {
    migrations, err := fs.Sub(os.DirFS("."), "schema/sqlite/migrations")
    if err != nil {
        return nil, err
    }
    db := sqlite.NewDatabase(dbPath, migrations)
    return anthropicmemory.NewSQLiteHandler(db)
}

func call(ctx context.Context, handler *anthropicmemory.Handler) error {
    client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))

    msg, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
        Betas:     []anthropic.AnthropicBeta{"context-management-2025-06-27"},
        MaxTokens: 1024,
        Model:     anthropic.ModelClaudeSonnet4_5,
        Messages: []anthropic.BetaMessageParam{
            anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Remember this project preference.")),
        },
        Tools: []anthropic.BetaToolUnionParam{
            {OfMemoryTool20250818: &anthropic.BetaMemoryTool20250818Param{}},
            // Equivalent helper:
            // anthropicmemory.Tool(),
        },
    })
    if err != nil {
        return err
    }

    for _, block := range msg.Content {
        if block.Type != "tool_use" || block.Name != anthropicmemory.ToolName {
            continue
        }
        input, err := anthropicmemory.DecodeInput(block.Input)
        if err != nil {
            return err
        }
        toolResult, err := handler.HandleToolUse(ctx, block.ID, input)
        if err != nil {
            return err
        }
        _ = toolResult // send this in the next user message as a tool_result block
    }
    return nil
}
```

See [`examples/anthropic-memory/`](../../examples/anthropic-memory/) for a runnable conversation loop.

## Storage, inspection, and backup

Traceary uses the normal DB path resolution for examples and CLI tools:

1. `--db-path` when a CLI command exposes it;
2. `TRACEARY_DB_PATH`;
3. `~/.config/traceary/traceary.db`.

The native memory-tool backend writes rows to `memory_tool_files`:

```sql
SELECT path, size_bytes, created_at, updated_at
FROM memory_tool_files
ORDER BY path;

SELECT path, CAST(content AS TEXT) AS content
FROM memory_tool_files
WHERE path = '/memories/project.md';
```

Back up the store with Traceary's normal backup command or SQLite tooling:

```sh
traceary store backup create --out traceary-backup.db
sqlite3 "$TRACEARY_DB_PATH" '.backup traceary-backup.db'
```

## Version pinning and upgrades

Traceary pins the tool contract to `memory_20250818` (`anthropicmemory.ToolVersion`). When Anthropic publishes a newer memory tool version, do **not** auto-bump the SDK field or serialized type. Upgrade only after manual review of:

- command names and JSON input shapes;
- return-string expectations;
- path and size semantics;
- any new security constraints or caller modes;
- migration compatibility for `memory_tool_files`.

Add a new handler/version if the contract is not backward-compatible.
