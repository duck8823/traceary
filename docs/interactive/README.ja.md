# インタラクティブ運用メモ

[English](./README.md)

この文書は、Traceary を対話的に使うときの使い勝手を整理するためのメモです。
`v0.1.9` で直接改善したものと、あえて後続 release line に送ったものを分けて記録します。

## `v0.1.9` で入れたもの

### Shell completion

Traceary は built-in の completion generator を持つようになりました。

```sh
traceary completion bash
traceary completion zsh
traceary completion fish
traceary completion powershell
```

これは、event model や read-path semantics を増やしすぎずに、日常の interactive 利用を改善できる最小の変更です。

## まだ後続送りにしているもの

次の案は有効ですが、`v0.1.9` には含めていません。

- `tail` / `watch` 的な live follow view
- `show` / `context` の人間向け整形強化
- pager-aware な output flow
- `list` / `search` の上に乗る、より opinionated な interactive filter

これらは小さな `v0.1.x` polish というより、`v0.2` の UX pass に近いと判断しています。

## 現時点の推奨

より豊かな interactive mode が入るまでは、次の使い分けを推奨します。

1. 最近の feed は `traceary list --limit ... --offset ...`
2. 条件付き lookup は `traceary search ... --json`
3. 構造化された詳細確認は `traceary show <event-id> --json`
4. command discovery は shell completion を有効化して補う

## 関連文書

- CLI reference: [`../cli/README.ja.md`](../cli/README.ja.md)
- MCP guide: [`../mcp/README.ja.md`](../mcp/README.ja.md)

