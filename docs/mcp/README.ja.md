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
| `list_events` | event listing。body は既定で 500 rune に切り詰める。`projection=metadata` で本文フィールドを省略し、`body_limit=0` / `full_body=true` で保存済み本文の全文を返す | read |
| `search` | literal text の event search。`list_events` と同じ metadata / bounded / full projection を使用できる | read |
| `get_context` | recent-context read。`list_events` と同じ metadata / bounded / full projection を使用できる | read |

`manage_memory.ids` は accept/reject flow 向けに単一 string と string array の両方を受け付けます。`record_event` は `type="log"` と `type="audit"` のどちらでも同じ shape を返します。

`list_events` と `search` は、日付だけの `from` / `to` に対して明示的な `timezone`（既定は UTC）を受け付けます。日付だけの `to` は指定した暦日を含み、RFC3339 の `to` は正確な排他時刻です。両 tool は追加の `interval` object を返し、要求された境界、開始を含み終了を含まない UTC の実効境界、タイムゾーン、`to` 省略時に使った 1 つのリクエストスナップショットを示します。

`session_status(action="tree", session_id="...", depth=N)` は `session_id` を root とする session subtree を `traceary session tree --json` と同じ node array shape で返します。`depth` は任意で、`0` は root のみを返します。

`session_status(action="active", ...)` は end marker 後にイベントを受け取った session を引き続き active として扱い、CLI `sessions --snapshot` の `ended_with_late_events` ルールと一致します。単独の `session_ended` の後に prompt や audit が続く場合、その session は active 結果から除外されません。

`session_status(action="handoff", ...)` と `query_memory(action="pack", ...)` は互換用の `recent_commands` 文字列配列を維持し、`recent_command_items` も返します。構造化された兄弟フィールドには `event_id`、本文を安全に短縮した `summary`、応答・保存・元データの byte 数、取り込み・保存・応答時の切り詰め情報、`retrieval_hint` が含まれます。不明な過去データの情報は省略します。保存済み本文の全文取得には `traceary show <event-id>` または event detail の明示的な呼び出しが必要で、handoff 自体は上限付きの本文先頭部分だけを読みます。

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
