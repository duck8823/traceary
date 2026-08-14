# MCP server（退役）

[English](./README.md)

Traceary の MCP server（`traceary mcp-server`）とその tool は **v0.35.0 で削除**されました（#1871）。

`traceary mcp-server` の呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。出荷ホスト package は MCP server を宣言しません。

同じ作業には CLI を使ってください。

| 旧 MCP 面 | CLI 置き換え |
|---|---|
| session latest --active / latest / handoff context | `traceary session latest --active`、`session latest`、`session handoff`、`context` |
| search / list events / report | `traceary search`、`list`、`report` |
| memory manage / query | `traceary memory store …`、`memory inbox …`、`memory admin …`、`memory search` |

hook capture はもともと shell（`traceary hook …`）で、変更はありません。Claude の `hooks.json` は `matcher: mcp__.*` を残し、*他サーバ*の audit を継続します。

ポリシー: 削除は one-minor deprecation window の明示的な例外です — [CLI 安定性](../cli-stability.ja.md) の過去の削除履歴を参照してください。
