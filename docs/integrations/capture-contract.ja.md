# Capture contract

[English](./capture-contract.md)

Traceary はコマンド / ツール実行を `command_executed` event と `command_audits` 行として記録します。この文書は **何を保存するか** の operator 向け契約です。

## 分類

| 分類 | 保存するもの | 対象 |
| --- | --- | --- |
| 全文キャプチャ | `command_text`、`input_text`、`output_text`（それぞれ `audit.max_input_bytes` / `audit.max_output_bytes` で上限をかけたあと redaction） | mutating ツール、shell、MCP（`mcp__*`）、未認識ツール、手動の `traceary audit`、および **失敗・denied の** 読み取り専用ツール |
| metadata のみ | 今日と同じ `command_text` / `input_text`。`output_text` は空。アクセス事実は `output_metadata` JSON | 分類済みの成功した read / list / grep（および Claude `WebFetch`） |

読み取り専用ツールの全文キャプチャを戻す opt-in はありません。`audit.max_output_bytes` は成功した metadata-only 行には **効きません**（出力本文を保存しないため）。metadata の `truncated` は、redaction 後の応答が設定上限を **超えていたかどうか** を報告します。

## metadata JSON

```json
{"bytes":12345,"capture":"metadata_only","paths":["README.md"],"sha256":"…","truncated":false}
```

- `paths`: tool input から取ったアクセス対象（最大 8、各 512 byte）。空なら省略
- `bytes`: **redaction 後** 応答の `len`
- `sha256`: 同じ redaction 後テキストの lower-case hex digest（redaction 前の秘密に対して取らない）
- `truncated`: redaction 後の全文が設定上限を超えたか
- metadata-only 行の `output_original_bytes` は `0` のまま。サイズは `output_metadata.bytes`

`show` は空の `OUTPUT:` 本文の代わりに metadata を出します。`list` のテキストモードはもともと audit 出力を出さないので追加しません。`list --json` は event envelope のまま（`command_audit` オブジェクトなし）。`show --json` は `output_metadata` を持ち、`"output": ""` を残します。

## host ごとの読み取り専用テーブル

分類は `domain/types/tool_access.go` の `(host, tool_name)` に対する exact / 大文字小文字区別の一致です。未知の host や未知のツールは全文キャプチャのままです。この表を第二の正本にしないでください。

| Host | 読み取り専用ツール | 注記 |
| --- | --- | --- |
| Claude | `Read`, `NotebookRead`, `Grep`, `Glob`, `WebFetch` | 確認済み。`WebSearch`、`Edit`/`Write`/`Bash`、`mcp__*` は全文 |
| Grok | `read_file`, `grep`, `list_dir` | 30 日分の operator store 集計で確認。`read_file` の path キーは `target_file` |
| Kimi | `Read`, `Grep`, `Glob`, `ReadMediaFile` | 同じ集計で確認。Kimi の `Read` は `file_path` ではなく `path` が多い |
| Gemini | `read_file`, `read_many_files`, `list_directory`, `glob`, `search_file_content` | 前方互換のための分類。現行 `AfterTool` matcher は `run_shell_command` のみなので、今日は audit hook に届かない |
| Codex | *(なし)* | 30 日集計は shell 風コマンド（argv としての `read`/`grep`/`ls`）のみ。Codex の read ツール名を推測すると shell 出力を落とす |
| Antigravity | *(なし)* | hook は `run_command` だけを合成し、全文キャプチャのまま |

失敗・denied の読み取り専用呼び出しは全文を残します（`audit.max_output_bytes` 上限）。MCP は全文（Traceary 自身の read MCP は hook suppression でそもそも記録しない）。

## 履歴行

既存の `output_text` は書き換えも purge もしません。古いバイナリは nullable な `output_metadata` 列を無視します。archive v1 セグメントはこの列を持てません。bundle の export/import はフィールドを運びます。

## 検索

新しい metadata-only 行はファイル本文では全文検索できません（本文はディスク上に残っています）。`command_text` と `input_text` はツール名と path を index します。
