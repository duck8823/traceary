# Cockpit UI/UX 設計（履歴）

[English](./cockpit-ui-ux-design.md)

> **履歴。** operator cockpit（`traceary tui` / `traceary dashboard` と、それを開いていた bare TTY 既定動作）は v0.34 の非推奨期間 (#1687) を経て v0.35.0 で削除されました (#1764)。bare `traceary` は常に help を表示します。存続する対話 / script 向け surface には `traceary list`、`traceary search`、`traceary tail`、`traceary doctor`、`traceary memory inbox review` を使ってください。孤立した local state ファイル `~/.local/state/traceary/cockpit.json`（または `$XDG_STATE_HOME/traceary/cockpit.json`）は手動で削除して安全です。

この文書は以前、cockpit の v0.17–v0.18 reference-driven 再設計目標を記録していました。パスは履歴のポインタとしてのみ残しています。現行の operator 案内としては扱わないでください。
