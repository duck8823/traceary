# Antigravity 移行状況

[English](./antigravity.md)

Antigravity は、Google の AI エージェントホストとして Gemini CLI の後継です。このページでは、v0.21.0 時点で Traceary がローカルで把握している Antigravity の情報と、その結果としての判断を説明します。

> **まとめ:** v0.21.0 では Antigravity の公開 CLI / hook contract は確認されていません。Antigravity の capability detection は #1195 で実装済みです。この調査の結果、**v0.21.0 では Antigravity の hook / package / release asset を意図的に提供しません** — Google が公開していない hook contract を Traceary が捏造することはありません。将来のサポートにはサポートされた公開 CLI/hook contract が必要です。既存の Gemini CLI インストールでは、引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用できます。

## ローカルでの調査結果（v0.21.0）

開発環境で確認された情報：

| プロパティ | 値 |
| --- | --- |
| アプリケーションパス | `/Applications/Antigravity.app` |
| Bundle ID | `com.google.antigravity` |
| バージョン | 2.1.4 |
| URL スキーム | `antigravity://` |
| PATH 上の CLI | 見つからず |
| ユーザーデータディレクトリ | `~/Library/Application Support/Antigravity` |
| 状態ヒント | `~/.gemini/antigravity`、`~/.gemini/config/config.json` |

## 機能検出（v0.21.0）

`traceary doctor --client antigravity --json` は Antigravity のインストール状況を調査し、以下の 4 つの機能ステートのいずれかを報告します：

| ステート | 意味 |
| --- | --- |
| `not_installed` | アプリバンドル（`/Applications/Antigravity.app`）も PATH 上の `antigravity` CLI も見つからない |
| `tool_unavailable` | アプリまたは CLI は見つかったが、サポートされた公開 headless/hook/package サーフェスが未確認 |
| `not_authenticated` | サポートされたサーフェスでインストールされているが、認証または設定が完了していない（将来/予約済み。v0.21.0 では未到達。認証情報を読み取るのではなく、サポートされた CLI/contract チェックで検出） |
| `available` | サポートされた CLI/hook contract が確認・設定済み（v0.21.0 では未到達） |

ローカル開発環境での現在のステートは **`tool_unavailable`** です：`/Applications/Antigravity.app`（バージョン 2.1.4）はインストールされていますが、公開 CLI や hook contract は確認されていません。実行例：

```sh
traceary doctor --client antigravity --json
```

このチェックはアプリを起動したり、ブラウザ自動操作や認証情報の読み取りを行いません。アプリバンドルと PATH 上の CLI バイナリの存在のみを確認します。

Antigravity はデフォルトの doctor クライアントリスト（`["claude","codex","gemini"]`）に含まれていません。`--client antigravity` を明示的に指定してください。

## v0.21.0 で未確認の事項

- 公開 CLI バイナリや hook contract は確認されていません。`antigravity` コマンドは PATH 上にありません。
- `gemini extensions install` に相当する extension / plugin インストール機構は確認されていません。
- Traceary 向けの hook イベントスキーマ（セッションライフサイクル、ツール監査、プロンプト/トランスクリプト取得）は未確立です。

## 判断とフォローアップ

- **#1195** ✓ — Antigravity 機能検出（`traceary doctor --client antigravity --json`）— v0.21.0 で実装済み
- **#1196** ✓ — 判断: v0.21.0 では Antigravity の hook / package / 生成メタデータ / release asset を**意図的に提供しません**。受け入れ条件では、サポートされた公開 hook/plugin/MCP/headless CLI サーフェスが確認できた**場合にのみ** package を追加することになっていました。#1195 で確認できなかったため、fake package を出荷せず、意図的に省略した package を文書化し、doctor の `tool_unavailable` ステートを維持します。

実際の Antigravity package は、Google がサポートされた公開 CLI/hook contract を公開した時点で、将来の issue で**初めて**追加します。それまでは、Antigravity セッションは Traceary のイベントログに記録されません。Gemini CLI から Antigravity へ移行中の場合は、Gemini CLI セッションについては引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用してください。
