# インタラクティブ運用メモ

[English](./README.md)

この文書では、現在の Traceary CLI を使って対話的に履歴を確認する方法を整理します。
書き込みの自動化よりも、人が参照系コマンドをどう使い分けるかに焦点を当てています。

## 最近変わったこと

現在の Traceary には、インタラクティブ利用を支える基本機能として次の 3 つがあります。

- Tail-first operator cockpit entrypoint としての bare `traceary`（明示的な互換 path として `traceary tui` も維持）
- shell completion
- `traceary tail` による live follow

そのため、参照系は `list` や `search` のような単発の snapshot だけではありません。
次に使うべきものが `top` / `tail` / `doctor` / `session handoff` / `memory inbox review` のどれかを覚えておきたくない場合は、まず cockpit を開くのが推奨です。

## いまの推奨フロー

確認したい内容に応じて、次のように使い分けるのがおすすめです。

### 1. 「まず1か所から始めたい」 → `traceary`

対話 terminal で Traceary の Tail-first operator cockpit を開きたいときは bare `traceary` を使います。`traceary tui` は同じ cockpit を明示的に開く互換 entrypoint として残ります。cockpit は active work、doctor の warning/failure、直近の失敗、前回 live tail 以降の新着 event、前回 memory review 以降のメモリ候補をまとめて表示します。そこから次の画面へ移動できます。

- live event tail
- doctor details
- memory inbox review

```sh
traceary
traceary tui
traceary tui --reset-state
```

cockpit は意図的に TTY 専用です。非対話シェルでは `traceary top --snapshot [--json]`、`traceary tail [--json]`、`traceary doctor --json`、`traceary session handoff`、`traceary memory inbox list` を使ってください。非 TTY の bare `traceary` は cockpit を起動せず、help と fallback guidance を表示します。

### 2. 「今なにが起きたか」をざっと見たい → `traceary list`

最近の履歴を素早く見たいときや、workspace / client などの構造 filter がすでに決まっているときは `list` を使います。

```sh
traceary list --limit 20
traceary list --workspace github.com/duck8823/traceary --client codex
```

### 3. 「今どの session が動いているか」を見たい → `traceary top`

ワークスペースの状況をライブの multi-pane dashboard で眺めたいときは `top` を使います。画面は次の 5 ペイン構成です。

- **sessions** — active session tree (workspace、agent role、latest event 時刻、latest event を `<kind>: <message>`)
- **failures** — 直近の失敗 `command_executed`
- **commands** — 直近の `command_executed`
- **candidates** — メモリ候補の確認キュー内のメモリ候補 (remember-intent priority 順)
- **stale memories** — cleanup が必要かもしれない accepted memory

```sh
traceary top
traceary top --workspace github.com/duck8823/traceary
traceary top --snapshot
traceary top --snapshot --json
```

dashboard 内では `tab` / `shift+tab` でフォーカスペインを切り替え、`↑/↓` (`k/j`) で 1 行ずつスクロール、`pgup/pgdn` でページング、`g/G` で先頭 / 末尾、`r` で snapshot 再取得、`?` でヘルプ切替、`q` / Ctrl-C / Esc は共通の安全網を経由して終了します。非 TTY (パイプ / CI ログ) では自動的に snapshot text 出力にフォールバックします。`--snapshot` / `--snapshot --json` も dashboard の 5 ペインに合わせて拡張されており、テキスト出力は `ACTIVE SESSIONS` / `RECENT FAILURES` / `RECENT COMMANDS` / `CANDIDATE MEMORIES (count=N)` / `STALE MEMORIES (count=N)` のセクション、JSON 出力は `sessions` / `failures` / `recent_commands` / `candidates` (`{ count, items }`) / `stale_memories` (`{ count, items }`) を持つ envelope オブジェクトを返します。

### 4. 「今まさに書き込まれているか」を追いたい → `traceary tail`

新しい event が入る様子をリアルタイムで追いたいときは `tail` を使います。hook が発火しているか、想定した workspace に書き込まれているか、失敗 event が流れてきているかを確認するのに向いています。

```sh
traceary tail
traceary tail --workspace github.com/duck8823/traceary --failures
traceary tail --json
```

### 5. 「特定のエラーやコマンドを探したい」 → `traceary search`

テキスト検索と期間・workspace 条件を組み合わせたいときは `search` を使います。

```sh
traceary search panic --workspace github.com/duck8823/traceary
traceary search --since 2026-04-01 --kind command_executed lint
```

### 6. 「構造化された詳細を見たい」 → `traceary show`

event ID が分かっていて、対応する event や audit payload を構造化して見たいときは `show` を使います。

```sh
traceary show evt_123 --json
```

### 7. 「メモリ候補を1件ずつ捌きたい」 → `traceary memory inbox review`

メモリ候補の確認キューを対話的に walk するときは `memory inbox review` を使います。TTY 必須なので、非対話シェルでは exit code `2` で起動を拒否し、`traceary memory inbox list / accept / reject / attach` の利用を案内します。フィルタ flag は snapshot 版と同じく `--workspace` / `--agent` / `--session-family` / `--type` / `--source` / `--include-hidden` / `--limit` を受け付けます。

```sh
traceary memory inbox review
traceary memory inbox review --workspace github.com/duck8823/traceary --type preference --limit 10
```

画面内のキーは `a` accept、`x` reject、`s` skip、`r` evidence 追加、`e` edit/distill、`v` evidence 表示、`?` help、`q` quit です。Accept / reject / evidence 追加は `memory inbox accept|reject|attach` と同じ application usecase を呼びます。`r` では `kind:value` 形式の evidence ref を1件入力し、evidence がない candidate を採用前に裏付けできます。`e` で開くエディトプロンプトは operator が手書きした fact のみ受け付け、`traceary memory store distill` 経由で記録します (LLM 出力は自動採用しません)。

### 8. 「次に持ち越す文脈だけをまとめたい」 → `traceary session handoff`

生の event stream ではなく、再開や引き継ぎに使う working-memory pack を見たいときは `session handoff` を使います。別エージェントへ渡す前提の要約を見るなら、まずここです（v0.13.x までの top-level alias `traceary handoff` は v0.14.0 で削除されました）。

```sh
traceary session handoff --workspace github.com/duck8823/traceary
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

## bare `traceary` entrypoint の方針

v0.19.0 では、stdin/stdout が対話 terminal に接続されている場合、bare `traceary` は Tail-first cockpit を開きます。非 TTY で subcommand なしの `traceary` を実行した場合は、Bubble Tea を起動せず deterministic な help / fallback output を維持します。

互換性の contract は次の通りです。

- `traceary tui` は、名前付き command を好む operator 向けの安定した明示 entrypoint として残す。
- 非 TTY の `traceary` は deterministic な help / script behavior を維持する。
- completion generation と help example を壊さない。
- script 向けには `list`、`top --snapshot [--json]`、`doctor --json`、`session handoff`、`memory inbox list` を推奨 path として維持する。
- release notes には default entrypoint の変更と、明示的な `traceary tui` 互換 path を書く。

## 今後の改善候補

初期の `v0.1.x` より interactive 利用はだいぶ良くなりましたが、次の改善余地はまだあります。

- `show` / `context` の人間向け整形強化
- pager-aware な output flow
- `list` / `search` の上に乗る、より opinionated な interactive filter

## 関連文書

- CLI reference: [`../cli/README.ja.md`](../cli/README.ja.md)
- MCP guide: [`../mcp/README.ja.md`](../mcp/README.ja.md)
- イベントライフサイクル: [`../lifecycle.ja.md`](../lifecycle.ja.md)
