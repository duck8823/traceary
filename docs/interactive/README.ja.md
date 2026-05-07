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

### 2. 「今どの session が動いているか」を見たい → `traceary top`

現在 active な session の tree をライブで眺めたいときは `top` を使います。各行に workspace、最も具体的な agent role、latest event 時刻、latest event を `<kind>: <message>` で表示するため、どの session が何をしているか一目で分かります。

```sh
traceary top
traceary top --workspace github.com/duck8823/traceary
traceary top --snapshot
```

### 3. 「今まさに書き込まれているか」を追いたい → `traceary tail`

新しい event が入る様子をリアルタイムで追いたいときは `tail` を使います。hook が発火しているか、想定した workspace に書き込まれているか、失敗 event が流れてきているかを確認するのに向いています。

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 4. 「特定のエラーやコマンドを探したい」 → `traceary search`

テキスト検索と期間・workspace 条件を組み合わせたいときは `search` を使います。

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 5. 「構造化された詳細を見たい」 → `traceary show`

event ID が分かっていて、対応する event や audit payload を構造化して見たいときは `show` を使います。

```sh
traceary show evt_123 --json
```

### 6. 「durable memory の候補を1件ずつ捌きたい」 → `traceary memory inbox review`

candidate inbox を対話的に walk するときは `memory inbox review` を使います。TTY 必須なので、非対話シェルでは exit code `2` で起動を拒否し、`traceary memory inbox list / accept / reject` の利用を案内します。フィルタ flag は snapshot 版と同じく `--workspace` / `--agent` / `--session-family` / `--type` / `--source` / `--include-hidden` / `--limit` を受け付けます。

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

画面内のキーは `a` accept、`x` reject、`s` skip、`e` edit/distill、`v` evidence 表示、`?` help、`q` quit です。Accept / reject は `memory inbox accept|reject` と同じ application usecase を呼びます。`e` で開くエディトプロンプトは operator が手書きした fact のみ受け付け、`traceary memory store distill` 経由で記録します (LLM 出力は自動採用しません)。

### 7. 「次に持ち越す文脈だけをまとめたい」 → `traceary handoff`

生の event stream ではなく、再開や引き継ぎに使う working-memory pack を見たいときは `handoff` を使います。別エージェントへ渡す前提の要約を見るなら、まずここです。

```sh
traceary handoff --workspace github.com/duck8823/traceary
traceary session handoff --session-id sess_123
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
