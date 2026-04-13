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
- MCP is best when the client should actively call tools such as `search`, `get_context`, or the explicit session tools

## Platform support

- `traceary mcp-server` follows the same support promise as the core CLI: actively tested on macOS and Linux
- prebuilt binaries are published for macOS and Linux; other Go-supported Unix-like environments may work via `go install`
- the standalone MCP server does not require `bash`, but hook integration still does
- native Windows support is not promised today; use WSL or another POSIX-compatible environment if you need it

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

Traceary currently exposes eighteen MCP tools.

### `start_session`

Records a `session_started` event.

Inputs:

- `client` (default: `mcp`)
- `agent` (default: `manual`)
- `session_id` (optional; Traceary generates one when omitted)
- `workspace` (optional work-context string)

### `end_session`

Records a `session_ended` event.

Inputs:

- `session_id` (required)
- `client` (optional; when omitted, Traceary prefers attribution from the matching `session_started` event)
- `agent` (optional; when omitted, Traceary prefers attribution from the matching `session_started` event)
- `workspace` (optional work-context string)

### `latest_session`

Returns the newest matching session.

Inputs:

- `client`
- `agent`
- `workspace`

### `active_session`

Returns the newest matching active session.

Inputs:

- `client`
- `agent`
- `workspace`
- `allow_stale` (default: `false`)
- `stale_after_seconds` (`0` or omitted uses the default `86400`)

### `list_events`

Returns recent events in reverse chronological order.

Inputs:

- `limit` (default: `20`)
- `offset` (default: `0`)

Use `list_events` when the client wants the same "recent feed" view as `traceary list`.
Use `search` when the client needs structured filters such as `workspace`, `session_id`, or a text query.

### `add_log`

Records a note-style event.

Inputs:

- `message` (required)
- `client` (default: `mcp`)
- `agent` (default: `manual`)
- `session_id` (default: `default`)
- `workspace` (optional work-context string)

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
- `workspace` (optional work-context string)

### `search`

Searches existing events.

Inputs:

- `query` (required)
- `workspace`
- `from` (`YYYY-MM-DD` or RFC3339)
- `to` (`YYYY-MM-DD` or RFC3339)
- `limit` (default: `20`)

### `get_context`

Returns recent raw events for context lookup.

Inputs:

- `workspace`
- `session_id`
- `limit` (default: `20`)

### `session_handoff`

Returns the structured working-memory handoff pack aligned with the CLI `traceary handoff` command.

The top-level `summary` field is kept for compatibility and mirrors `working_state.combined_summary`.

Inputs:

- `workspace`
- `session_id`
- `recent_commands_limit` (default: `5`; explicit `0` disables recent commands)
- `memory_limit` (default: `5`; explicit `0` disables durable memories)

### `retrieve_memories`

Returns durable memories by direct ID lookup, full-text query, or scope filters.

Inputs:

- `memory_id`
- `query`
- `workspace`
- `agent`
- `session_family`
- `status`
- `type`
- `limit` (default: `20`)
- `offset` (default: `0`)

When `memory_id` is set, Traceary returns that single memory with evidence and artifact refs.
When `query` is set, Traceary performs full-text search.
Otherwise it lists active memories, optionally filtered by scope / status / type.

### `remember_memory`

Records an accepted durable memory.

Inputs:

- `type` (required)
- one of `workspace` / `agent` / `session_family` (required, mutually exclusive)
- `fact` (required)
- `confidence`
- `source`
- `evidence_refs`
- `artifact_refs`

Accepted memories require at least one evidence ref.

### `propose_memory`

Records a candidate durable memory that still needs review.

Inputs:

- `type` (required)
- one of `workspace` / `agent` / `session_family` (required, mutually exclusive)
- `fact` (required)
- `source`
- `evidence_refs`
- `artifact_refs`

### `accept_memory`

Accepts a candidate durable memory.

Inputs:

- `memory_id` (required)
- `confidence`

### `reject_memory`

Rejects a candidate durable memory.

Inputs:

- `memory_id` (required)

### `supersede_memory`

Marks an accepted durable memory as superseded and stores a replacement accepted memory.

Inputs:

- `memory_id` (required)
- `fact` (required)
- replacement `type`
- replacement `workspace` / `agent` / `session_family`
- `confidence`
- `source`
- `evidence_refs`
- `artifact_refs`

Replacement `type` / scope inherit from the current memory when omitted.

### `expire_memory`

Expires a durable memory.

Inputs:

- `memory_id` (required)
- `expires_at` (`YYYY-MM-DD` or RFC3339; defaults to now)

### `memory_pack`

Builds a memory-aware context pack for prompt-context enrichment or automation.

Inputs:

- `workspace`
- `session_id`
- `recent_commands_limit` (default: `5`; explicit `0` disables recent commands)
- `memory_limit` (default: `5`; explicit `0` disables durable memories)

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
3. let the client call `active_session` or `latest_session` when it needs to resume a session explicitly
4. let the client call `list_events` when it wants a recent feed without filters
5. let the client call `get_context` before a new task
6. let the client call `search` when it needs old command output or notes
7. optionally use `start_session` / `end_session` / `add_log` / `add_audit` when the client should manage session lifecycle itself

This keeps passive ingestion and active context lookup in one local store.

## Related docs

- hooks ingestion guide: [`../hooks/README.md`](../hooks/README.md)
- release/install guide: [`../release/README.md`](../release/README.md)
