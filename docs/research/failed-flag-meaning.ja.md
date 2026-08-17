# 決定: `failed=1` の意味 (#1767)

[English](./failed-flag-meaning.md)

**Status:** 決定済み。`list --failures` は残す。

**Date:** 2026-08-15

**Issue:** #1767

## 決定

`command_audits.failed = 1` は、構造化されたコマンド失敗の互換フラグです。現行の書き込み経路では `failure_reason.IsFailure()` から導出し、独立には立てません。`list --failures`（および `search` / `list --follow` の同じ述語）は `failed = 1` **または** 取得済みの非ゼロ `exit_code` に一致します。host は今もほぼ exit code を出さないため、生きている面はフラグ側です。

この面は残します。`doctor` には移しません。`failed=1 AND failure_reason='unknown'` を禁じる CHECK は追加しません。restore が分類器以前の行を保持する必要があるためです。新規書き込みはその組を保存できません。

## 回答

### 1. `unknown` は分類器以前の行に漏れた schema default か

はい。migration `000025_normalize_command_audits.sql` が `failure_reason TEXT NOT NULL DEFAULT 'unknown'` を足しました。`11691479`（2026-07-22, `feat: normalize command audit outcomes`）より前は、hook audit が `Failed` を `hookPayloadFailed` から立て、reason を分類しなかったので DEFAULT が載りました。その commit 以降、構造化された hook 失敗は `host_error` です。同日の `b1daa0e7` が `Failed` を `failureReason.IsFailure()` にしたので、フラグと reason は揃います。

`unknown` 自体は失敗理由ではありません。`CommandFailureReason.IsFailure()` は `unknown` と `none` で false です。

### 2. 2026-07-21/24 前後に何が変わり、今も `unknown` + `failed=1` は書けるか

分類器は 2026-07-22 に入りました。その直前で `unknown`+`failed=1` が止まり、直後から `host_error` が始まる live corpus は upgrade 窓であり、共存する二種類の失敗ではありません。

現行の書き込みはその組を残せません。

- `hookPayloadFailureReason`: 構造化 host error → `host_error`。根拠なし → `unknown` かつ `Failed=false`。
- `ClassifyOutcome`: `unknown` + 構造化失敗かつ exit code なし → `host_error`。
- restore（`CommandAuditFromSnapshot`）だけが `unknown` + `failed=true` を受け付けるので、履歴行は読めます。

### 3. 現行の `host_error` はどこから来るか

仕組みは一つ、host は複数、常に `client=hook` です。

| Host | 構造化シグナル | Reason |
|---|---|---|
| Claude Code | `PostToolUseFailure` のトップレベル `error` | `host_error` |
| Gemini CLI | `tool_response.error`（spawn/OS のみ。単なる非ゼロ shell exit は出ない） | `host_error` |
| Codex | 構造化失敗フィールドなし | フラグされない |
| Grok | `PermissionDenied` / 厳密な `🚫 [hook]` marker | `hook_denied`（`host_error` ではない） |
| Interrupt / timeout marker | `is_interrupt`, `timed_out`, … | `signal` / `timeout` |

これはソース契約であり、live store の件数ではありません。

### 4. operator はこれらを `list --failures` で見たいか

はい。`host_error` は host が報告したツール失敗（Claude の `PostToolUseFailure`、Gemini の spawn error）です。これは 記録であり、`hook_denied` / `signal` / `timeout` / `exit_code` と同じクラスです。`doctor` には retry-loop 診断が既にあり、`--failures` を doctor に畳むと audit trail が隠れます。分類器以前の `unknown`+`failed=1` は述語の `failed=1` 側で見えます。

### 5. 未分類失敗を不可能にする CHECK を足すべきか

いいえ。既存 CHECK は許可する reason 文字列を列挙しています。`NOT (failed = 1 AND failure_reason = 'unknown')` のような制約は履歴行の restore を拒否します。書き込み経路は未分類の構造化失敗を既に `host_error` へ上げます。

## 対象外

- `list --failures` の削除・非推奨化
- live store の照会や書き換え
- 履歴の `unknown` 行を `host_error` へ backfill すること
