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
| `TRACEARY_REPO` | 補助的な work-context identifier を上書きする |
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

## Runtime 前提

- Traceary は local-first で、データは現在の machine の SQLite に保存します
- core CLI と `traceary mcp-server` は macOS / Linux を主対象として検証しています
- release archive も現状は macOS / Linux 向けです
- hooks は現在 `bash` と Unix-like shell semantics を前提にしています
- `git` は任意です。使える場合、hook script は `remote.origin.url` を保存用の `repo` field に正規化します
- Windows の native PowerShell / `cmd.exe` 向け hook 実行はまだ正式対応していません。Windows で hooks を使う場合は WSL などの POSIX 互換環境を使ってください

## Privacy posture

- Traceary はホスト型サービスを前提にしていません
- Traceary 自身の backend へ telemetry を送信しません
- `traceary audit` の payload は、redaction / truncation がかからない限り、ローカル SQLite store に保存されます
- secret redaction はベストエフォートであり、完全な DLP ではありません

## 関連ドキュメント

- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップ手順: [`../backup/README.ja.md`](../backup/README.ja.md)
