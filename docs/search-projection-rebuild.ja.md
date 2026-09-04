# Search projection rebuild

[English](search-projection-rebuild.md)

search-projection サブシステムは v0.49.0（#2319）で削除されました。世代 lifecycle、`store compact --projection-rebuild` / `--projection-abort`、doctor の budget / parked / terminal-rows 検査はありません。

`traceary search` は #2318 の two-tier 読み取り経路（refinement 優先 + canonical な `events` / `command_audits` / `sessions` への未索引 fallback 走査）を使います。query が空の filter-only 検索は、同じ表の構造走査です。削除前の詳細は `docs/research/search-projection-*` の研究ノートを参照してください。
