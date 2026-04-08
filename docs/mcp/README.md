# MCP integration

[日本語](./README.ja.md)

Traceary can expose its local SQLite history as a stdio MCP server.
Use this when another AI client should read or write Traceary data through MCP tools instead of shelling out to the CLI directly.

## Choose the right integration path

Use the simplest path that matches your workflow.

| Need | Best path |
| --- | --- |
| You want to record or inspect data from shell scripts or by hand | direct CLI (`traceary log`, `traceary audit`, `traceary search`, ...) |
| You want Claude Code / Codex CLI / Gemini CLI to ingest session boundaries and shell-command audits automatically | hooks |
| You want an MCP-capable client to query past context or write events through tools | `traceary mcp-server` |

Hooks and MCP are complementary:

- hooks are best for passive ingestion of session starts, session ends, and shell audits
- MCP is best when the client should actively call tools such as `search` or `get_context`

## Start the server

The MCP server uses stdio.
It does not open a network port.

```sh
traceary mcp-server
```

To point at a non-default SQLite file, use either `TRACEARY_DB_PATH` or `--db-path`.

```sh
TRACEARY_DB_PATH=/path/to/traceary.db traceary mcp-server
traceary mcp-server --db-path /path/to/traceary.db
```

All commands resolve the DB path in this order:

1. `--db-path`
2. `TRACEARY_DB_PATH`
3. `~/.config/traceary/traceary.db`

`traceary mcp-server --help` currently shows:

```text
Run the Traceary MCP server over stdio

Usage:
  traceary mcp-server [flags]

Flags:
      --db-path string   SQLite DB path (env: TRACEARY_DB_PATH)
  -h, --help             help for mcp-server
```

## Exposed tools

Traceary currently exposes four MCP tools.

### `add_log`

Records a note-style event.

Inputs:

- `message` (required)
- `client` (default: `mcp`)
- `agent` (default: `manual`)
- `session_id` (default: `default`)
- `repo` (optional work-context string)

### `add_audit`

Records a shell-command audit event.

Like the CLI, `add_audit` redacts common secret-like values before they are written to SQLite. Treat that redaction as best-effort, not a complete guarantee. The MCP surface intentionally does not expose an `allow-secrets` override; use the direct CLI only when you intentionally want raw payload persistence.

Inputs:

- `command` (required)
- `input`
- `output`
- `client` (default: `mcp`)
- `agent` (default: `manual`)
- `session_id` (default: `default`)
- `repo` (optional work-context string)

### `search`

Searches existing events.

Inputs:

- `query` (required)
- `repo`
- `from` (`YYYY-MM-DD` or RFC3339)
- `to` (`YYYY-MM-DD` or RFC3339)
- `limit` (default: `20`)

### `get_context`

Returns recent events for context handoff.

Inputs:

- `repo`
- `session_id`
- `limit` (default: `20`)

## Practical client example

Many stdio MCP clients accept an `mcpServers` entry like this:

```json
{
  "mcpServers": {
    "traceary": {
      "command": "traceary",
      "args": ["mcp-server"],
      "env": {
        "TRACEARY_DB_PATH": "/Users/you/.config/traceary/traceary.db"
      }
    }
  }
}
```

If your client uses a different config shape, translate the same three pieces:

- command: `traceary`
- args: `["mcp-server"]`
- optional env: `TRACEARY_DB_PATH=/path/to/traceary.db`

## Suggested workflow

One practical pattern is:

1. use hooks to record session boundaries and command audits automatically
2. connect the same Traceary DB through MCP
3. let the client call `get_context` before a new task
4. let the client call `search` when it needs old command output or notes
5. optionally use `add_log` or `add_audit` for client-side annotations that did not come from hooks

This keeps passive ingestion and active context lookup in one local store.

## Related docs

- hooks ingestion guide: [`../hooks/README.md`](../hooks/README.md)
- release/install guide: [`../release/README.md`](../release/README.md)
