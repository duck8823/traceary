# Capture contract

[日本語](./capture-contract.ja.md)

Traceary records command and tool executions as `command_executed` events plus a `command_audits` row. This document is the operator-facing contract for **what is stored**.

## Classes

| Class | What is stored | Who |
| --- | --- | --- |
| Full capture | `command_text`, `input_text`, and `output_text` (each bounded by `audit.max_input_bytes` / `audit.max_output_bytes`, then redacted) | Mutating tools, shell, MCP (`mcp__*`), unrecognised tools, the manual `traceary audit` command, and **failed or denied** read-only tools |
| Metadata only | `command_text` and `input_text` as today; `output_text` empty; `output_metadata` JSON with access facts | Successful classified read / list / grep (and Claude `WebFetch`) |

There is no opt-in switch that restores full capture for read-only tools. `audit.max_output_bytes` does **not** apply to successful metadata-only rows: the row stores no output text. The metadata `truncated` flag still reports whether the redacted response **would have** exceeded the configured cap.

## Metadata JSON

```json
{"bytes":12345,"capture":"metadata_only","paths":["README.md"],"sha256":"…","truncated":false}
```

- `paths`: access targets taken from the tool input (at most 8, each at most 512 bytes). Omitted when empty.
- `bytes`: `len` of the **redacted** response.
- `sha256`: lower-case hex digest of that same redacted text (never of the pre-redaction secret).
- `truncated`: the full redacted response exceeded the configured cap.
- `output_original_bytes` on the row stays `0` for metadata-only captures. Size lives in `output_metadata.bytes`.

`show` prints the metadata instead of an empty `OUTPUT:` body. `list` text mode never rendered audit output; `list --json` stays an event envelope (no `command_audit` object). `show --json` carries `output_metadata` and keeps `"output": ""`.

## Per-host read-only table

Classification is an exact, case-sensitive match on `(host, tool_name)` in `domain/types/tool_access.go`. Unknown host or unknown tool keeps full capture. Do not treat this table as a second source of truth.

| Host | Read-only tools | Notes |
| --- | --- | --- |
| Claude | `Read`, `NotebookRead`, `Grep`, `Glob`, `WebFetch` | Verified. `WebSearch`, `Edit`/`Write`/`Bash`, `mcp__*` stay full. |
| Grok | `read_file`, `grep`, `list_dir` | Confirmed from a 30-day operator-store aggregate. Path key for `read_file` is `target_file`. |
| Kimi | `Read`, `Grep`, `Glob`, `ReadMediaFile` | Confirmed from the same aggregate. Kimi `Read` often uses `path`, not `file_path`. |
| Gemini | `read_file`, `read_many_files`, `list_directory`, `glob`, `search_file_content` | Classified for forward compatibility. Current `AfterTool` matcher is `run_shell_command` only, so these names never reach the audit hook today. |
| Codex | *(none)* | 30-day aggregates showed shell-like commands only (`read`/`grep`/`ls` as argv, not host tools). Guessing a Codex read-tool name would drop shell output. |
| Antigravity | *(none)* | The hook synthesises `run_command` only; that stays full capture. |

Failed / denied read-only calls keep full capture (bounded by `audit.max_output_bytes`). MCP tools keep full capture (Traceary's own read MCP tools are already skipped entirely by hook suppression).

## Historical rows

Existing `output_text` is never rewritten or purged. Older binaries ignore the nullable `output_metadata` column. Archive v1 segments cannot carry the column; archived metadata-only rows lose the access facts. Bundle export/import does carry the field.

## Search

New metadata-only rows are not full-text searchable by file contents (the contents remain on disk). `command_text` and `input_text` still index the tool name and the path.
