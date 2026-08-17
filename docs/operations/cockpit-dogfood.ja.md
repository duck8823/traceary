# Cockpit dogfood チェックリスト（履歴）

[English](./cockpit-dogfood.md)

> **履歴。** operator cockpit とその dogfood golden suite は v0.35.0 で削除されました (#1764)。`TestCockpitDogfood` を実行したり、このチェックリストを release gate として扱ったりしないでください。残る対話 surface には必要に応じて `traceary list`、`traceary search`、`traceary memory inbox review`、`traceary tail` を dogfood してください。孤立した `~/.local/state/traceary/cockpit.json`（または `$XDG_STATE_HOME/traceary/cockpit.json`）は手動で削除して安全です。

このパスは、旧 `traceary tui` 向け release dogfood 手順への履歴ポインタとしてのみ残しています。
