# MCP integration

[English](./README.md)

Traceary は `traceary mcp-server` でローカル SQLite 履歴を stdio MCP server として公開します。
AI client が CLI を直接呼ばずに Traceary data を tool 経由で読み書きしたい場合に使います。

## 公開 tools

Traceary が公開する MCP tool はちょうど 8 個です（v0.10.0 で固定し、golden snapshot `presentation/mcpserver/testdata/tool_registry.golden.json` で保証）:

| Tool | Actions / shape | Mode |
|---|---|---|
| `manage_memory` | `propose`, `remember`, `accept`, `reject`, `expire`, `supersede`, `set_validity`, `import_instructions` | write; destructive subset: `reject`, `expire` |
| `query_memory` | `retrieve`, `export`, `pack`, `scan_hygiene` | read |
| `manage_session` | `start`, `end` | write |
| `session_status` | `active`, `latest`, `handoff`, `tree` | read |
| `record_event` | `type="log"` or `type="audit"` | write |
| `list_events` | event listing。body は既定で 500 rune に truncate (`body_limit` / `full_body=true` で上書き可) | read |
| `search` | literal text の event search。body は既定で 500 rune に truncate (`body_limit` / `full_body=true` で上書き可) | read |
| `get_context` | recent-context read。body は既定で 500 rune に truncate (`body_limit` / `full_body=true` で上書き可) | read |

`manage_memory.ids` は accept/reject flow 向けに単一 string と string array の両方を受け付けます。`record_event` は `type="log"` と `type="audit"` のどちらでも同じ shape を返します。

`session_status(action="tree", session_id="...", depth=N)` は `session_id` を root とする session subtree を `traceary session tree --json` と同じ node array shape で返します。`depth` は任意で、`0` は root のみを返します。

### Search query semantics

`search.query` は literal text query であり、boolean query language ではありません。`failure OR timeout` のような文字列は `failure` または `timeout` の any-match expression として解釈されず、1つの検索文字列として扱われます。複数語を調べたい場合は、より狭い `search` call を複数回実行するか、CLI JSON output を local file に保存して `jq` などの local tools で集計してください。

将来 any-match を追加する場合は、`query` を暗黙に拡張するのではなく、例えば additive な `any_terms` field のような明示的な minor-version contract として追加します。

## v0.10.0 移行表 (24 → 8 tools)

| 旧 tool | 新しい呼び出し |
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

## 例

```json
{"tool":"manage_memory","arguments":{"action":"propose","type":"constraint","workspace":"github.com/org/repo","fact":"Never push directly to main"}}
```

```json
{"tool":"query_memory","arguments":{"action":"retrieve","query":"main","limit":5}}
```

```json
{"tool":"record_event","arguments":{"type":"log","message":"handoff note","kind":"note","session_id":"s1"}}
```
