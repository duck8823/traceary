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

1. **Discovery:** 現在の `workspace` から始めます。`list_events` では、
   分かっている `from` / `to` または `session_id` filter を追加し、
   `projection="metadata"` と `5` 程度の小さい `limit` を使います。絞り込んだ
   literal `search` には workspace と time filter を使えますが、
   `session_id` input はありません。session filter を使えるのは
   `list_events` と `get_context` だけです。これにより、event body を読まずに
   candidate ID と metadata を取得できます。
2. **Inspection:** `list_events` または `search` を、各 tool が対応する
   Discovery filter と `300`–`500` 程度の正の `body_limit` で再実行します。
   `get_context` は上限付きの周辺 context を読む場合だけ使います。
   `event_id`、kind、time-range filter はないため、対象 event を絞る入力は
   `workspace`、`session_id`、`limit` だけです。選んだ event ID を直接取得する
   tool ではなく、広い範囲の最初の read にも使いません。
3. **Detail:** 保存済み body の全文は、調査理由を明示して 1 件の event を
   選んだ場合だけ取得します。明示的な CLI detail path である
   `traceary show <event-id>` を優先します。MCP の history read tool には
   `event_id` input がないためです。広い history query に対して
   `full_body=true` や `body_limit=0` から始めてはいけません。

session recap でも同じ順序を使います。metadata を発見してから選んだ context を
確認し、上限付きの evidence だけでは recap を支えられない場合にだけ detail を
取得します。

### Durable memory hygiene の continuation

`query_memory(action="scan_hygiene")` は row、source byte、result byte、
comparison、duration に有限の上限を適用します。partial response には暗号化済みの
`next_cursor` が含まれます。cursor は実行中の MCP process が所有する AES-GCM key
で認証され、memory fact の平文を含みません。server 再起動後は利用できず、認証に
失敗した場合は新しい scan が必要であることを明示します。旧 checksum cursor は
受理しません。

`consistency=consistent` は continuation chain が 1 つの memory revision に
留まったことを示します。ページ間に hook または別 client が memory を書き込むと、
次の call は保持済み keyset を維持し、現在 revision に束縛し直して、
`consistency=best_effort` / `consistency_reason=revision_changed` へ恒久的に
downgrade したうえで、同じ source page を残りの実行予算内で再試行します。
再試行が成功した場合、後で実際に停止させた上限を `stop_reason` で返します。
page が前進する前に revision 変更が繰り返されて duration を使い切った場合は、
partial response が `stop_reason=revision_changed` と最新の確認済み revision に
束縛した cursor を返します。以後のページも best-effort marker を維持します。
これは read-only scan の振る舞いです。mutation path は、何かを適用する前に
1 つの revision で対象を完全に再検証します。

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
