# 対話的な使い方

[English](./README.md)

このメモは、いま同梱されている CLI で Traceary を対話的に確認する方法を説明します。
write-side の hook 自動化ではなく、人間向けの read-side workflow に焦点を当てます。

## 最近変わったこと

Traceary は次の対話向け convenience を同梱しています。

- bare `traceary` は help を表示（TTY / 非 TTY とも）。旧 Tail-first cockpit entrypoint は v0.35.0 で削除
- shell completion
- live-follow 向けの `traceary list --follow`
- 直近作業向けの `traceary report` / `traceary list` / `traceary search`
- TTY 専用 inbox walk-through の `traceary memory inbox review`

つまり interactive read path は `list` / `search` のような one-shot snapshot に限定されません。

## 推奨する対話 workflow

答えたい質問に応じて次の command を使い分けてください。

### 1. 「使える command を見たい」→ `traceary`

bare `traceary`（および `traceary --help`）は常に help を表示します。実作業では明示的な read command を優先してください。

```sh
traceary
traceary --help
traceary list
traceary search
traceary doctor --json
```

旧 operator cockpit（`traceary tui` / `traceary dashboard` と、それを開いていた bare TTY 既定動作）は v0.35.0 で削除されました。孤立した local state ファイル `~/.local/state/traceary/cockpit.json`（または `$XDG_STATE_HOME/traceary/cockpit.json`）は手動で削除して安全です。

### 2. 「いま何が起きた？」→ `traceary list`

structured filter がすでに分かっているときの直近 feed に `list` を使います。

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

<a id="3-which-sessions-are-running-right-now--traceary-top"></a>

### 3. 「いま動いている session は？」→ `traceary list` / `report`

`traceary sessions` は v0.42.0 で削除されました。open session の ID は hook メッセージ（`[Traceary] Session <id>`）で渡ります。直近の作業は `list` / `search` / `context`、期間サマリーは `report` です。

```sh
traceary list --limit 20
traceary search --workspace github.com/duck8823/traceary
traceary report
```

### 4. 「いま event が書かれているか？」→ `traceary list --follow`

新しい event をリアルタイムで追うときは `list --follow` を使います。
hook が発火しているか、想定 workspace に書き込まれているか、失敗が起きているかを確認するのに向いています。

```sh
traceary list --follow
traceary list --follow --workspace github.com/duck8823/traceary --failures
traceary list --follow --json
```

### 5. 「特定の error / command / note を探す」→ `traceary search`

テキスト検索と時間 / workspace filter を組み合わせるときは `search` を使います。

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 6. 「構造化レコード全体を見る」→ `traceary show`

event ID がすでに分かっているとき、structured event または audit payload を見るには `show` を使います。

```sh
traceary show evt_123 --json
```

### 7. 「メモリ候補を順に確認する」→ `traceary memory inbox review`

memory review queue を対話的に walk するときは `memory inbox review` を使います。TTY 専用で、非対話 shell は exit code `2` で拒否し、`traceary memory inbox list / accept / reject / attach` を案内します。snapshot view と同じ filter（`--workspace`、`--agent`、`--session-family`、`--type`、`--source`、`--include-hidden`、`--limit`）を受け付けます。

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

画面内の action key は `a` accept、`x` reject、`s` skip、`r` attach evidence、`e` edit/distill、`v` view evidence、`?` help、`q` quit です。Accept / reject / evidence attach は `memory inbox accept|reject|attach` と同じ application use case を再利用します。`r` は comma-separated な `kind:value` evidence ref と任意の `artifact:kind:value` ref を受け付け、evidence の無い候補を accept 前に補強できます。`e` は operator-authored fact の入力を要求する editor prompt を開き、`traceary memory store distill` 経由で処理します（LLM 出力の auto-accept はありません）。

### 8. 「次の session に何を持ち込む？」→ `traceary context --handoff`

生 event stream ではなく concise な working-memory pack が欲しいときは `context --handoff` を使います。
作業再開や他 agent への handoff 向けの operator-facing summary view です（`traceary session handoff` は v0.42.0 で削除。v0.13.x の top-level `traceary handoff` alias は v0.14.0 で削除済み）。

```sh
traceary context --handoff --workspace github.com/duck8823/traceary
traceary context --handoff --session-id sess_123
```

## Shell completion

Traceary は built-in completion generator を提供します。

```sh
traceary completion bash
traceary completion zsh
traceary completion fish
traceary completion powershell
```

`list --follow` が入ったあとも、より広い CLI surface の discovery 摩擦を下げるため completion は有効にしておく価値があります。

## bare `traceary` entrypoint の方針

bare `traceary` は TTY / 非 TTY とも常に help を表示します。旧 Tail-first cockpit 既定と `traceary tui` / `traceary dashboard` は v0.35.0 で削除されました (#1764)。

互換 contract は次のとおりです。

- bare `traceary` と `traceary --help` は help のみを表示する
- completion generation と help の例は安定したままにする
- automation の推奨 path は script-facing command（`list`、`list --follow`、`search`、`doctor --json`、`context --handoff`、`memory inbox list`）

## まだ future-facing なもの

対話的な作業は early `v0.1.x` より良くなりましたが、次はまだ将来の UX pass の対象です。

- `show` / `context` のより読みやすい human-readable formatting
- pager を意識した出力 flow
- `list` / `search` 上のより opinionated な interactive filter

## 関連 docs

- CLI リファレンス: [`../cli/README.ja.md`](../cli/README.ja.md)
- Event lifecycle: [`../lifecycle.ja.md`](../lifecycle.ja.md)
