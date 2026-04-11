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
| `TRACEARY_HOOK_SCRIPTS_DIR` | portable hook script の書き出し先を上書きする |
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
| `redact.extra_patterns` | 文字列配列 | 監査リダクション用の追加正規表現パターン。各エントリは Go の `regexp` パターンとしてコンパイルされ、マッチした内容が `[REDACTED]` に置換されます。CLI（`traceary audit`）と MCP サーバー（`add_audit`）の両方で、組み込みルールの後に適用されます。 |

例:

```json
{
  "redact": {
    "extra_patterns": ["my_custom_secret", "internal_auth_header:\\s*\\S+"]
  }
}
```

ファイルが存在しない場合、Traceary は組み込みのリダクションパターンのみを使用します。
ファイルが存在しても unreadable または不正な JSON の場合、Traceary は組み込みのリダクションパターンへ fallback し、config ベースの redaction が必要な場面では operator 向け warning を出し、`traceary doctor` でもその壊れた状態を報告します。

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
- secret redaction はベストエフォートであり、完全な DLP ではありません

## 関連ドキュメント

- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップ手順: [`../backup/README.ja.md`](../backup/README.ja.md)
