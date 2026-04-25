# Anthropic native memory tool (experimental)

[English](./anthropic-memory-tool.md)

Traceary は Anthropic の native beta memory tool (`memory_20250818`) の backend を Go コードから提供できます。Handler は `pkg/anthropicmemory` にあり、tool が扱う filesystem 形状の状態を local SQLite の `memory_tool_files` table に保存します。

この連携は v0.10 では **experimental** です。Anthropic の memory tool が beta の間、公開 Go API は変わる可能性があります。

## Native memory tool と Traceary MCP memory tools の使い分け

native Anthropic memory tool を使う場面:

- Anthropic Messages API / Go SDK を直接呼んでいる;
- Claude 自身に `view` / `create` / `str_replace` / `insert` / `delete` / `rename` の実行タイミングを判断させたい;
- `/memories/...` 配下の filesystem 形状・model-managed memory でよい。

Traceary の MCP memory tools (`traceary mcp-server`, `manage_memory`, `query_memory`) を使う場面:

- agent host が MCP に対応している (Claude Code, Codex, Gemini CLI, OpenAI Agents SDK, Google ADK など);
- status / confidence / evidence / validity window / review-accept workflow を持つ operator-curated durable memory が必要;
- 複数 model provider で同じ interface を使いたい。

2 つの store は意図的に分離しています。`memory_tool_files` は native memory-tool の file backend、`memories` は Traceary の durable knowledge aggregate です。

## Threat model と limits

- **Path traversal**: v0.10-50 の handler は保存前に全 path を検証します。path は canonical な `/memories/...` である必要があります。relative path、`..`、backslash traversal、URL-encoded traversal、`/memories` 外の absolute path は SQLite に届く前に拒否されます。
- **Size limits**: memory-tool backend は content を SQLite `BLOB` として保存します。Traceary は renderer 用の command-level line limit (999,999 lines) を持ち、listing 用に `size_bytes` を cache します。ただし local untrusted model output として扱い、DB サイズを監視し、通常の SQLite backup を取り、任意の non-Anthropic input に handler を公開しないでください。
- **Client-side execution**: Anthropic memory tool は client tool です。model が command payload を出し、application が実行し、`tool_result` を返します。

## Go SDK wiring

SDK を追加 / 更新します:

```sh
go get github.com/anthropics/anthropic-sdk-go@v1.38.0
```

Beta Messages API に pinned memory tool を登録します:

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
            // helper でも同じ:
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
        _ = toolResult // 次の user message に tool_result block として入れる
    }
    return nil
}
```

実行可能な会話 loop は [`examples/anthropic-memory/`](../../examples/anthropic-memory/) を参照してください。

## Storage, inspection, backup

example / CLI は通常の DB path 解決を使います:

1. CLI command が提供する場合は `--db-path`;
2. `TRACEARY_DB_PATH`;
3. `~/.config/traceary/traceary.db`。

Native memory-tool backend は `memory_tool_files` に row を書きます:

```sql
SELECT path, size_bytes, created_at, updated_at
FROM memory_tool_files
ORDER BY path;

SELECT path, CAST(content AS TEXT) AS content
FROM memory_tool_files
WHERE path = '/memories/project.md';
```

Backup は Traceary の通常 backup command か SQLite tooling を使います:

```sh
traceary store backup create --out traceary-backup.db
sqlite3 "$TRACEARY_DB_PATH" '.backup traceary-backup.db'
```

## Version pinning and upgrades

Traceary は tool contract を `memory_20250818` (`anthropicmemory.ToolVersion`) に pin します。Anthropic が新しい memory tool version を公開しても、SDK field や serialized type を自動 bump しないでください。以下を manual review してから upgrade します:

- command 名と JSON input shape;
- return string 期待値;
- path / size semantics;
- 新しい security constraint や caller mode;
- `memory_tool_files` の migration compatibility。

Contract が backward-compatible でなければ、新しい handler/version を追加してください。
