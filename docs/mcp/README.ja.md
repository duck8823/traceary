# MCP integration

[English](./README.md)

Traceary は `traceary mcp-server` でローカル SQLite 履歴を stdio MCP server として公開します。
AI client が CLI を直接呼ばずに Traceary data を tool 経由で読み書きしたい場合に使います。

## 公開 tools

Traceary が公開する MCP tool は 9 個で、golden snapshot `presentation/mcpserver/testdata/tool_registry.golden.json` で保証します。元の 8 tool は v0.10.0 から v0.29.x まで固定し、v0.30.0 で読み取り専用の集計 tool `get_report` を追加します。

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
| `get_report` | 本文を読まない session/event/command/usage 集計。データ源ごとの完全・部分情報を返す | read |

`manage_memory.ids` は accept/reject flow 向けに単一 string と string array の両方を受け付けます。`record_event` は `type="log"` と `type="audit"` のどちらでも同じ shape を返します。

`list_events` と `search` は、日付だけの `from` / `to` に対して明示的な `timezone`（既定は UTC）を受け付けます。日付だけの `to` は指定した暦日を含み、RFC3339 の `to` は正確な排他時刻です。両 tool は追加の `interval` object を返し、要求された境界、開始を含み終了を含まない UTC の実効境界、タイムゾーン、`to` 省略時に使った 1 つのリクエストスナップショットを示します。

`get_report` は CLI の `traceary report --json` と同じ応答スキーマを使い、usage と重複排除済み run fact の集計も含みます。`page_size` は 1 以上 100,000 以下で、本文を含まない SQLite 内部読み取りのページサイズだけを変え、既定は全件集計です。正の `result_cap` を指定した場合だけ、データ源ごとの部分集計を明示的に要求します。部分集計は観測件数・時刻範囲と `truncation_reason=result_cap` を返し、不完全な分母に基づく割合は省略します。usage 値は既知・取得不能の件数、加算対象外の会計証拠、provider reported と estimated の cost origin の分離を保持します。

`session_status(action="tree", session_id="...", depth=N)` は `session_id` を root とする session subtree を `traceary session tree --json` と同じ node array shape で返します。`depth` は任意で、`0` は root のみを返します。

`session_status(action="active", ...)` は end marker 後にイベントを受け取った session を引き続き active として扱い、CLI `sessions --snapshot` の `ended_with_late_events` ルールと一致します。単独の `session_ended` の後に prompt や audit が続く場合、その session は active 結果から除外されません。

`session_status(action="handoff", ...)` と `query_memory(action="pack", ...)` は互換用の `recent_commands` 文字列配列を維持し、`recent_command_items` も返します。構造化された兄弟フィールドには `event_id`、本文を安全に短縮した `summary`、応答・保存・元データの byte 数、取り込み・保存・応答時の切り詰め情報、`retrieval_hint` が含まれます。不明な過去データの情報は省略します。保存済み本文の全文取得には `traceary show <event-id>` または event detail の明示的な呼び出しが必要で、handoff 自体は上限付きの本文先頭部分だけを読みます。

### 段階的な event 取得

過去の session、event、command audit を調査するときは、次の順序で読み取ります。

1. **Discovery:** 現在の `workspace` から始め、分かっている `from` / `to`
   または `session_id` filter を追加します。`list_events`（または絞り込んだ
   literal `search`）を `projection="metadata"` と `5` 程度の小さい `limit`
   で呼びます。これにより、event body を読まずに candidate ID と metadata を
   取得できます。
2. **Inspection:** 選んだ filter を維持し、文脈が必要な candidate だけを
   `300`–`500` 程度の正の `body_limit` で確認します。`get_context` は event
   または同等に狭い scope を選んだ後にだけ使います。広い範囲の最初の read には
   使いません。
3. **Detail:** 保存済み body の全文は、調査理由を明示して 1 件の event を
   選んだ場合だけ取得します。明示的な CLI detail path である
   `traceary show <event-id>` を優先します。広い history query に対して
   `full_body=true` や `body_limit=0` から始めてはいけません。

session recap でも同じ順序を使います。metadata を発見してから選んだ context を
確認し、上限付きの evidence だけでは recap を支えられない場合にだけ detail を
取得します。

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
| — | `get_report(...)`（v0.30.0 で追加） |

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
