# ドキュメント索引

[English](./README.md)

このページは Traceary の詳細ドキュメント一覧です。トップの README だけでは足りなくなったときに、ここから目的別の文書をたどれるようにしています。

## まず読むページ

- [アーキテクチャ原則](./architecture/README.ja.md): 4 層、runtime 境界、`scripts/` の役割、`internal/` の方針
- [Durable memory ガイド](./memory/README.ja.md): 3 層モデル、memory のライフサイクル、ref の意味、memory コマンドの関係
- [CLI リファレンス](./cli/README.ja.md): コマンドごとの挙動、主要フラグ、出力仕様
- [Hook contract](./hooks/contract.ja.md): ホストごとの自動記録範囲と共通動作
- [イベントライフサイクル](./lifecycle.ja.md): session 開始、audit、prompt、summary がどう event になるか
- [環境変数リファレンス](./environment/README.ja.md): 環境変数、実行時の前提、対応プラットフォーム
- [ストレージモデル](./storage/README.ja.md): SQLite の構造、マイグレーション、GC、保存しないもの

## 連携まわり

- [ネイティブ連携ガイド](./integrations/README.ja.md): Claude / Codex / Gemini 向けパッケージ、導入手順、smoke test
- [Hooks ガイド](./hooks/README.ja.md): Claude Code / Codex / Gemini の hook 設定、導入手順、トラブルシュート
- [MCP ガイド](./mcp/README.ja.md): `traceary mcp-server` の起動方法、ツール一覧、MCP クライアント連携
- [インタラクティブ運用ガイド](./interactive/README.ja.md): `list`、`tail`、`search`、`show`、`handoff` の使い分け

## 運用

- [バックアップガイド](./backup/README.ja.md): バックアップ、復元、マシン移行の手順
- [運用上の前提](./operations/README.ja.md): SQLite の同時実行、hook 状態管理、既知の制約
- [Python 依存の縮小計画](./operations/python-dependencies.ja.md): 現在の Python helper、影響範囲、移行順
- [リリースガイド](./release/README.ja.md): リリース手順、GitHub Actions、ローカルでの確認方法

## リポジトリの基本文書

- [README](../README.ja.md): インストール、クイックスタート、主要コマンド
- [コントリビューションガイド](../CONTRIBUTING.ja.md): ローカル検証、PR の進め方、脆弱性の連絡先
- [変更履歴](../CHANGELOG.ja.md): バージョンごとの変更点

## このリポジトリの文書ルール

人向けの Markdown は、英語版と日本語版をセットで管理します。

- 英語版を基準のファイル名とする
- 日本語版には `.ja.md` を付ける
- 更新するときは同じ PR で両言語をそろえる
- 言語切り替えリンクは各文書の冒頭付近に置く

CI では `python3 scripts/verify_docs_i18n.py` を実行し、文書ペアと言語切り替えリンクの整合を確認しています。
