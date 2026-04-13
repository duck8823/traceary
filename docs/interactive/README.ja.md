# インタラクティブ運用メモ

[English](./README.md)

この文書では、現在の Traceary CLI を使って対話的に履歴を確認する方法を整理します。
書き込みの自動化よりも、人が参照系コマンドをどう使い分けるかに焦点を当てています。

## 最近変わったこと

現在の Traceary には、インタラクティブ利用を支える基本機能として次の 2 つがあります。

- shell completion
- `traceary tail` による live follow

そのため、参照系は `list` や `search` のような単発の snapshot だけではありません。

## いまの推奨フロー

確認したい内容に応じて、次のように使い分けるのがおすすめです。

### 1. 「今なにが起きたか」をざっと見たい → `traceary list`

最近の履歴を素早く見たいときや、workspace / client などの構造 filter がすでに決まっているときは `list` を使います。

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

### 2. 「今まさに書き込まれているか」を追いたい → `traceary tail`

新しい event が入る様子をリアルタイムで追いたいときは `tail` を使います。hook が発火しているか、想定した workspace に書き込まれているか、失敗 event が流れてきているかを確認するのに向いています。

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 3. 「特定のエラーやコマンドを探したい」 → `traceary search`

テキスト検索と期間・workspace 条件を組み合わせたいときは `search` を使います。

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 4. 「構造化された詳細を見たい」 → `traceary show`

event ID が分かっていて、対応する event や audit payload を構造化して見たいときは `show` を使います。

```sh
traceary show evt_123 --json
```

### 5. 「次に持ち越す文脈だけをまとめたい」 → `traceary handoff`

生の event stream ではなく、再開や引き継ぎに使う working-memory pack を見たいときは `handoff` を使います。別エージェントへ渡す前提の要約を見るなら、まずここです。

```sh
traceary handoff --workspace github.com/duck8823/traceary
traceary session handoff --session-id sess_123 --json
```

## Shell completion

Traceary には built-in の completion generator があります。

```sh
traceary completion bash
traceary completion zsh
traceary completion fish
traceary completion powershell
```

`tail` が入った後でも、CLI 全体の発見しやすさを上げる意味で completion を有効にする価値はあります。

## 今後の改善候補

初期の `v0.1.x` より interactive 利用はだいぶ良くなりましたが、次の改善余地はまだあります。

- `show` / `context` の人間向け整形強化
- pager-aware な output flow
- `list` / `search` の上に乗る、より opinionated な interactive filter

## 関連文書

- CLI reference: [`../cli/README.ja.md`](../cli/README.ja.md)
- MCP guide: [`../mcp/README.ja.md`](../mcp/README.ja.md)
- イベントライフサイクル: [`../lifecycle.ja.md`](../lifecycle.ja.md)
