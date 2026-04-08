# ドキュメント索引

[English](./README.md)

このページは Traceary の詳細ドキュメント一覧です。トップの README だけでは足りなくなったら、ここから辿ってください。

## まず読むページ

- [CLI リファレンス](./cli/README.ja.md): コマンドごとの挙動、主要フラグ、出力仕様
- [環境変数リファレンス](./environment/README.ja.md): 環境変数、実行時の前提、対応プラットフォーム
- [保存モデル](./storage/README.ja.md): SQLite の構造、マイグレーション、GC、保存しないもの

## 連携まわり

- [ネイティブ連携ガイド](./integrations/README.ja.md): Claude / Codex / Gemini 向け package、導入手順、smoke test
- [Hooks ガイド](./hooks/README.ja.md): Claude Code / Codex / Gemini の hook 設定、導入手順、トラブルシュート
- [MCP ガイド](./mcp/README.ja.md): `traceary mcp-server` の起動方法、ツール一覧、MCP クライアント連携
- [インタラクティブ運用メモ](./interactive/README.ja.md): 日常利用で効いた改善点と、その判断理由

## 運用

- [バックアップガイド](./backup/README.ja.md): バックアップ、復元、マシン移行の手順
- [運用上の前提](./operations/README.ja.md): SQLite の同時実行、hook 状態管理、既知の制約
- [リリースガイド](./release/README.ja.md): リリース手順、GitHub Actions、ローカルでの確認方法

## リポジトリの基本文書

- [README](../README.ja.md): インストール、クイックスタート、主要コマンド
- [コントリビューションガイド](../CONTRIBUTING.ja.md): ローカル検証、PR の進め方、脆弱性の連絡先
- [変更履歴](../CHANGELOG.ja.md): バージョンごとの変更点

## このリポジトリの文書ルール

人が読む Markdown は、英語版と日本語版をセットで管理します。

- 英語版を基準ファイル名にする
- 日本語版は `.ja.md` を付ける
- 更新するときは同じ PR で両言語をそろえる
- 言語切り替えリンクは各文書の冒頭付近に置く

CI では `python3 scripts/verify_docs_i18n.py` を実行し、文書ペアと言語切り替えリンクを検証しています。
