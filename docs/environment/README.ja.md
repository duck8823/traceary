# 環境変数と実行時の前提

[English](./README.md)

このページでは、Traceary の環境変数、実行時の前提、公開上の対応方針をまとめています。

## DB と CLI 向け環境変数

| Variable | Purpose |
| --- | --- |
| `TRACEARY_DB_PATH` | 全 CLI コマンドで使う SQLite DB path を上書きする |
| `TRACEARY_LANG` | operator 向け CLI message を切り替える (`en` 既定, `ja` 対応) |
| `TRACEARY_CLIENT` | `log` / `audit` / session command の既定 `client` attribution |
| `TRACEARY_AGENT` | `log` / `audit` / session command の既定 `agent` attribution |
| `TRACEARY_SESSION_ID` | `log` / `audit` / `session end` の既定 session ID |
| `TRACEARY_WORKSPACE` | 補助的な work-context identifier を上書きする |
| `TRACEARY_ALLOW_SECRETS` | `traceary audit` の best-effort secret redaction を無効化する |
| `TRACEARY_MAX_AUDIT_INPUT_BYTES` | `traceary audit` の input 保存サイズ上限の既定値 |
| `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` | `traceary audit` の output 保存サイズ上限の既定値 |

## Hook 関連の環境変数

| Variable | Purpose |
| --- | --- |
| `TRACEARY_BIN` | generated hooks が使う `traceary` binary path を上書きする |
| `TRACEARY_HOOK_STATE_DIR` | hook session state の一時保存先 directory を上書きする |
| `TRACEARY_HOOK_STATE_KEY` | 既定 key が合わないときに process ごとの state key を上書きする |

## Logging / diagnostics 関連の環境変数

| Variable | Purpose |
| --- | --- |
| `LOG_LEVEL` | structured log の verbosity を設定する (`debug`, `info`, `warn`, `error`) |
| `LOG_OPTION` | `development` を指定すると source 付き text log、既定は JSON log |

## 設定ファイル

Traceary はオプションの JSON 設定ファイルを `~/.config/traceary/config.json` から読み込みます。

| キー | 型 | 用途 |
| --- | --- | --- |
| `redact.extra_patterns` | 文字列配列 | 監査および transcript リダクション用の追加正規表現パターン。各エントリは Go の `regexp` パターンとしてコンパイルされ、マッチした内容が `[REDACTED]` に置換されます。CLI（`traceary audit`、`traceary log --kind transcript`、Claude Stop-hook の transcript capture）と MCP サーバー（`add_audit`、`kind=transcript` を指定した `add_log`）の両方で、組み込みルールの後に適用されます。 |
| `read.fields` | 文字列配列 | `traceary tail` / `list` / `search` のテキスト出力で `--fields` が指定されなかった場合に使用されるコンパクトカラムのデフォルト順。利用可能なフィールド名: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`。未知・空・重複エントリはコマンド実行時に拒否されます。`--fields` フラグが指定された場合は常にこの設定を上書きします。`--wide` や `--json` 出力には影響しません。 |
| `read.presets` | object | `traceary tail` / `list` / `search` 向けの保存済みビュー。`--preset <name>` で適用します。各 entry は `fields`（`read.fields` と同じ registry）と `filters`（`kind`, `failures`, `workspace`, `session_id`, `client`, `agent`）を持てます。明示した CLI フラグは常に preset を上書きします。built-in preset（`failures`, `prompts-only`, `compact-summaries`）と同名のエントリは built-in を上書きしますが、実行時に stderr へ `[WARN]` を出します。 |
| `read.color` | string | `traceary tail` / `list` / `search` のコンパクトテキスト出力に対する `--color` の既定値。許容値: `auto`, `always`, `never`。`auto` は stdout が TTY のときだけ色を付けます。`NO_COLOR` 環境変数や明示の `--color=never` は常にこの設定より優先されます。`--wide` / `--json` 出力には色は付きません。 |

例:

```json
{
  "redact": {
    "extra_patterns": ["my_custom_secret", "internal_auth_header:\\s*\\S+"]
  },
  "read": {
    "fields": ["ts", "kind", "session", "ws", "message"],
    "presets": {
      "my-view": {
        "fields": ["ts", "kind", "message"],
        "filters": {
          "kind": "prompt",
          "failures": true
        }
      }
    }
  }
}
```

ファイルが存在しない場合、Traceary は組み込みのデフォルトを config 由来の全機能で使用します。
ファイルが存在しても unreadable または不正な JSON の場合、Traceary は組み込みのデフォルトへ fallback し、config 由来の機能が使われる場面では operator 向け warning を出し、`traceary doctor` でもその壊れた状態を報告します。

## Runtime 前提

- Traceary は local-first で、データは現在の machine の SQLite に保存します
- core CLI と `traceary mcp-server` は macOS / Linux を主対象として検証しています
- release archive も現状は macOS / Linux 向けです
- hooks は現在 `bash` と Unix-like shell semantics を前提にしています
- `git` は任意です。使える場合、Traceary はまず `remote.origin.url` を正規化して使い、無い場合はローカル Git worktree のルートへ fallback して work-context を解決します
- Windows の native PowerShell / `cmd.exe` 向け hook 実行はまだ正式対応していません。Windows で hooks を使う場合は WSL などの POSIX 互換環境を使ってください

## Privacy posture

- Traceary はホスト型サービスを前提にしていません
- Traceary 自身の backend へ telemetry を送信しません
- `traceary audit` の payload は、redaction / truncation がかからない限り、ローカル SQLite store に保存されます
- `prompt` イベント（`UserPromptSubmit` hook 経由）と `compact_summary` イベント（`PostCompact` hook 経由）は、redaction / truncation なしでそのまま保存されます — ユーザーの意図を記録することが Traceary の目的であるため、これは設計上の選択です
- `transcript` イベント（Claude `Stop` hook、`traceary log --kind transcript`、MCP `add_log` の `kind=transcript` 経由）は、`audit` と同じ方式で redaction されます（組み込み redactor + `redact.extra_patterns`）。assistant の transcript は shell 出力やファイル内容を再掲することが多く、secret を含む可能性が高いためです
- secret redaction はベストエフォートであり、完全な DLP ではありません

## 関連ドキュメント

- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップ手順: [`../backup/README.ja.md`](../backup/README.ja.md)
