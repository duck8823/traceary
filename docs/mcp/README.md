# MCP integration

[日本語](./README.ja.md)

Traceary exposes its local SQLite history as a stdio MCP server via `traceary mcp-server`.
Use MCP when an AI client should read/write Traceary data through tools instead of shelling out to the CLI.

## Exposed tools

Traceary v0.10.0 exposes exactly 8 MCP tools:

| Tool | Actions / shape | Mode |
|---|---|---|
| `manage_memory` | `propose`, `remember`, `accept`, `reject`, `expire`, `supersede`, `set_validity`, `import_instructions` | write; destructive subset: `reject`, `expire` |
| `query_memory` | `retrieve`, `export`, `pack`, `scan_hygiene` | read |
| `manage_session` | `start`, `end` | write |
| `session_status` | `active`, `latest`, `handoff`, `tree` | read |
| `record_event` | `type="log"` or `type="audit"` | write |
| `list_events` | unchanged event listing | read |
| `search` | unchanged event search | read |
| `get_context` | unchanged recent-context read | read |

`manage_memory.ids` accepts either a single string or an array of strings for accept/reject flows. `record_event` returns one uniform shape for both `type="log"` and `type="audit"`.

`session_status(action="tree", session_id="...", depth=N)` returns the JSON session subtree rooted at `session_id` using the same node array shape as `traceary session tree --json`; `depth` is optional and `0` returns only the root.

## v0.10.0 migration map (24 → 8 tools)

| Old tool | New call |
|---|---|
| `propose_memory` | `manage_memory(action="propose", ...)` |
| `remember_memory` | `manage_memory(action="remember", ...)` |
| `accept_memory` | `manage_memory(action="accept", ids="<id>", ...)` |
| `reject_memory` | `manage_memory(action="reject", ids="<id>")` |
| `expire_memory` | `manage_memory(action="expire", ids="<id>", ...)` |
| `supersede_memory` | `manage_memory(action="supersede", target_id="<id>", fact="...", ...)` |
| `set_memory_validity` | `manage_memory(action="set_validity", ids="<id>", valid_from="...", valid_to="...", ...)` |
| `import_memory_instructions` | `manage_memory(action="import_instructions", ...)` |
| `accept_memories_batch` | `manage_memory(action="accept", ids=[...], ...)` |
| `reject_memories_batch` | `manage_memory(action="reject", ids=[...])` |
| `retrieve_memories` | `query_memory(action="retrieve", ...)` |
| `export_memories` | `query_memory(action="export", ...)` |
| `memory_pack` | `query_memory(action="pack", ...)` |
| `scan_memory_hygiene` | `query_memory(action="scan_hygiene", ...)` |
| `start_session` | `manage_session(action="start", ...)` |
| `end_session` | `manage_session(action="end", ...)` |
| `active_session` | `session_status(action="active", ...)` |
| `latest_session` | `session_status(action="latest", ...)` |
| `session_handoff` | `session_status(action="handoff", ...)` |
| `session tree --json --root <session-id>` | `session_status(action="tree", session_id="<session-id>", ...)` |
| `add_log` | `record_event(type="log", ...)` |
| `add_audit` | `record_event(type="audit", ...)` |
| `list_events` | `list_events(...)` |
| `search` | `search(...)` |
| `get_context` | `get_context(...)` |

## Examples

```json
{"tool":"manage_memory","arguments":{"action":"propose","type":"constraint","workspace":"github.com/org/repo","fact":"Never push directly to main"}}
```

```json
{"tool":"query_memory","arguments":{"action":"retrieve","query":"main","limit":5}}
```

```json
{"tool":"record_event","arguments":{"type":"log","message":"handoff note","kind":"note","session_id":"s1"}}
```
