# Antigravity 移行状況

[English](./antigravity.md)

Antigravity は、Google の AI エージェントホストとして Gemini CLI の後継です。このページでは、v0.21.0 時点で Traceary がローカルで把握している Antigravity の情報と、残っているフォローアップ作業を説明します。

> **まとめ:** v0.21.0 では Antigravity の公開 CLI / hook contract は確認されていません。Traceary の Antigravity hook / package 実装は #1195（hook 配線）と #1196（extension package）で追跡中です。既存の Gemini CLI インストールでは、引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用できます。

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

## v0.21.0 で未確認の事項

- 公開 CLI バイナリや hook contract は確認されていません。`antigravity` コマンドは PATH 上にありません。
- `gemini extensions install` に相当する extension / plugin インストール機構は確認されていません。
- Traceary 向けの hook イベントスキーマ（セッションライフサイクル、ツール監査、プロンプト/トランスクリプト取得）は未確立です。

## フォローアップ

- **#1195** — Antigravity hook 配線（セッション、ツール監査、プロンプト/トランスクリプト取得）
- **#1196** — Traceary 向け Antigravity extension package

これらのイシューが解決されるまで、Antigravity セッションは Traceary のイベントログに記録されません。Gemini CLI から Antigravity へ移行中の場合は、Gemini CLI セッションについては引き続き [Gemini CLI extension](./gemini-extension.ja.md) を使用してください。
