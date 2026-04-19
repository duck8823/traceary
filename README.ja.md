# Traceary

[English](./README.md)

<p align="center">
  <img src="./docs/assets/traceary-mark.svg" alt="Traceary mark" width="120">
</p>

[![CI](https://github.com/duck8823/traceary/actions/workflows/ci.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/ci.yml)
[![Release](https://github.com/duck8823/traceary/actions/workflows/release.yml/badge.svg?event=push)](https://github.com/duck8823/traceary/actions/workflows/release.yml)

Traceary は、AI エージェントの作業記録をローカルの SQLite に残し、あとから検索・再利用できる CLI / MCP サーバーです。セッションの開始と終了、実行したコマンド、補助メモ、引き継ぎ用の要約をひとつのストアにまとめて扱えます。

普段から自動で記録を残したいなら、CLI を手で打ち始めるよりも、Claude / Codex / Gemini への組み込みから始めるのがおすすめです。

## Traceary が役立つ場面

AI を使った開発では、次のような困りごとが起こりがちです。

- `clear` や `compact` のあとに、直前までの文脈が見えなくなる
- Git 履歴を見れば「何を変えたか」は分かっても、「なぜそうしたか」は追いにくい
- どのエージェントが、どのコマンドを、どのセッションで実行したのか確認しづらい
- Claude、Codex、Gemini、手元のターミナル操作の記録が別々に散らばる
- 並列セッションや worktree の切り替えで、履歴の流れが追いにくくなる

Traceary は、こうした記録をローカルの 1 つのストアに集約し、CLI・hooks・MCP のどこからでも同じ履歴を扱えるようにします。

## 3 層モデル

Traceary は、単なるイベントログではありません。`v0.5.0` 以降は、AI エージェントの実運用に合わせて次の 3 層で整理しています。

| 層 | 何を置くか | 役割 |
|---|---|---|
| Audit / Archive | 生の event、session 境界、command audit | 監査・検索・後追い確認のための元データを残す |
| Working memory | 直近の session から組み立てる handoff / context pack | 再開時や別エージェントへの引き継ぎに必要な文脈だけを取り出す |
| Durable memory | decision / constraint / preference / artifact ref など | セッションをまたいで再利用したい事実だけを明示的に保持する |

つまり Traceary は、ログをためるだけの CLI ではなく、AI エージェント向けの local-first な記憶基盤として使えます。

Traceary はローカルファーストです。データは手元の SQLite に保存され、組み込みのテレメトリ送信やホスト型ストレージはありません。

## はじめかた

### Step 1: Traceary CLI をインストールする

先に CLI が必要です。各エージェント向けのプラグインや hook も、最終的には `traceary` バイナリを呼び出します。

```sh
# Homebrew（推奨）
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary

# または go install
go install github.com/duck8823/traceary@latest
```

タグ付きリリースでは macOS / Linux 向けアーカイブを [GitHub Releases](https://github.com/duck8823/traceary/releases) に公開しています。配布形態の詳細は [リリースガイド](./docs/release/README.ja.md) を参照してください。

### Step 2: エージェント向けパッケージを入れる

**Claude Code** ([ガイド](./docs/integrations/claude-plugin.ja.md))

```sh
claude plugins marketplace add https://github.com/duck8823/traceary
claude plugins install traceary@traceary-plugins --scope user
```

**Codex** ([ガイド](./docs/integrations/codex-plugin.ja.md))

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
cd ~/src/traceary
codex   # Codex 内で /plugins を開き、Traceary Plugins から Traceary を install
```

旧コマンド `traceary integration codex install` も過渡的に使えますが、v0.8.0 以降のタイミングで削除予定の非推奨経路です。移行方法は [Codex plugin ガイド](./docs/integrations/codex-plugin.ja.md) を参照してください。

**Gemini CLI** ([ガイド](./docs/integrations/gemini-extension.ja.md))

```sh
bash <(curl -sL https://raw.githubusercontent.com/duck8823/traceary/main/scripts/install-gemini-extension.sh)
```

全体像は [ネイティブ連携ガイド](./docs/integrations/README.ja.md) にまとめています。

### Step 3: 設定を確認する

```sh
traceary doctor
```

## クイックスタート

`traceary init` は必須ではありません。通常コマンドを実行すれば、必要に応じて DB の作成とマイグレーションが自動で行われます。
`init` を使うのは、保存先を先に作っておきたいときや、書き込み権限を事前に確認したいときだけです。

### 1. セッションを開始してメモを残す

```sh
sid=$(traceary session start --client dogfood --agent codex)
event_id=$(traceary log --client dogfood --agent codex --session-id "$sid" --id-only "失敗したテストを調査している")
traceary show "$event_id" --json
```

### 2. 同じセッションにコマンド実行結果を残す

```sh
traceary audit \
  --client dogfood \
  --agent codex \
  --session-id "$sid" \
  --command "go test ./..." \
  --input '{"stdin":""}' \
  --output '{"stdout":"panic: boom","stderr":"stacktrace","exitCode":1}'

traceary search boom --json
traceary session active
```

### 3. スクリプトからは `--id-only` を使う

```sh
traceary log --id-only "失敗したテストを調べている"
traceary audit --id-only --command "go test ./..." --input '{}' --output '{}'
traceary session end --session-id "$sid" --id-only
```

### 4. 再利用したい事実は durable memory に残す

```sh
traceary memory remember \
  --type decision \
  --workspace github.com/duck8823/traceary \
  --fact "再開時の要約には traceary handoff を使う" \
  --evidence issue:#502

traceary handoff --workspace github.com/duck8823/traceary
```

## 直近の動きを確認する

Traceary は 4 つの補完的なビューを用意していて、「いま何が起きているか」と「ある期間に何が起きたか」をターミナルから切り替えて確認できます。

| 目的 | コマンド | 使いどころ |
|---|---|---|
| いま動いているものを追う | `traceary tail` | hook が発火しているか / 失敗がリアルタイムで見えているかを確認 |
| ある期間の流れを俯瞰する | `traceary timeline` | アイドルギャップ区切りの作業ブロックを workspace 別のアクティビティ要約付きで表示 |
| 生 event を直接掘る | `traceary list` / `traceary search` | kind / session / query をピンポイントで指定 |
| 引き継ぎコンテキストで再開する | `traceary handoff` | 整形済みの working memory を次のセッションへ |

### `traceary tail`

```text
$ traceary tail --limit 3
07:06:44  command_executed  sess=4a70c526  ws=traceary  ls ~/.traceary 2>&1; find ~ -name "traceary…
07:06:47  command_executed  sess=4a70c526  ws=traceary  ./traceary timeline --db-path /Users/duck88…
07:06:52  command_executed  sess=4a70c526  ws=traceary  timeout 1 ./traceary tail --db-path /Users/…
```

デフォルトは 1 行コンパクト形式（現地時刻）で約 100 カラムに収まります。`--wide --utc` で v0.6.1 以前の tab 区切り 7 カラムをバイト単位で再現でき、`--json` を使えば NDJSON でパイプに流せます。

### `traceary timeline`

```text
$ traceary timeline --limit 2
2026-04-15 06:37 - 07:06 (29m21s) total events: 165
  github.com/duck8823/traceary (153) — 自律的に進めてください。
  github.com/duck8823/dotfiles  ( 12) — rust インストールしました
2026-04-15 05:39 - 06:10 (31m1s) total events: 136
  github.com/duck8823/traceary (136) — <analysis> This conversation is a resumption after compaction. …
```

各ブロックは workspace ごとの 1 行サブロウで表示され、アクティビティ要約は **`compact_summary` → 最初の `prompt` → kind counts** のフォールバック順で選ばれるため、どの workspace で何が行われていたかを一目で把握できます。`--utc` でタイムスタンプを UTC に切り替え、`--json` を使うと既存フィールドに加えて `workspace_breakdown` 配列が返ります。

詳細なフラグリファレンスと追加例は [`docs/cli/README.ja.md`](./docs/cli/README.ja.md) を参照してください。

## ホスト別の自動記録マトリクス

問い合わせ面は共通です。Traceary を入れれば、どのホストからでも同じ CLI / MCP の memory・context 機能を使えます。差が出るのは、hook でどこまで自動記録できるかです。

| ホスト | セッション境界 | ツール監査 | prompt 記録 | compact summary 記録 | 自動記録の対応レベル |
|---|---|---|---|---|---|
| Claude Code | 完全対応 | Bash + MCP + failure hook | あり | あり | Full |
| Codex | 完全対応（`SessionStart` + `Stop`） | tool hook | あり | なし | Partial |
| Gemini CLI | 完全対応（`SessionStart` + `SessionEnd`） | tool hook | なし | なし | Basic |

詳しい契約と hook の意味付けは、[Hook contract](./docs/hooks/contract.ja.md) と [イベントライフサイクル](./docs/lifecycle.ja.md) を参照してください。

## 先に知っておくと楽なこと

- `traceary log` / `traceary audit` で `--session-id` を省くと、解決できた workspace に対応する最新の non-stale アクティブセッションを優先して使います。`remote.origin.url` が無い Git worktree では、worktree ルートパスを代わりに使います
- `traceary session active` は既定で 24 時間を超えたセッションを stale とみなします。必要なら `--allow-stale` を付けてください
- `traceary session start` はセッション ID を出力し、`traceary session end` は記録したイベント ID を出力します
- `traceary session list --json` では、値がある場合に `label` / `summary` / `parent_session_id` も確認できます
- CLI の通常メッセージは英語が既定です。日本語表示にしたい場合は `TRACEARY_LANG=ja` を指定してください
- `--json` 出力は言語設定の影響を受けません

## ドキュメント

詳しい一覧は [ドキュメント索引](./docs/README.ja.md) にまとめています。最初によく使うのは次のページです。

- [ネイティブ連携ガイド](./docs/integrations/README.ja.md)
- [アーキテクチャ原則](./docs/architecture/README.ja.md)
- [Durable memory ガイド](./docs/memory/README.ja.md)
- [CLI リファレンス](./docs/cli/README.ja.md)
- [Hooks ガイド](./docs/hooks/README.ja.md)
- [Hook contract と対応レベル](./docs/hooks/contract.ja.md)
- [イベントライフサイクル](./docs/lifecycle.ja.md)
- [MCP ガイド](./docs/mcp/README.ja.md)
- [環境変数と保存モデル](./docs/environment/README.ja.md)

## コントリビュートとサポート

- バグ報告や改善提案は GitHub Issues へお願いします
- 脆弱性の報告方法は [SECURITY.ja.md](./SECURITY.ja.md) を参照してください
- まだ `v0.x` の OSS なので、自動化に組み込む前には [変更履歴](./CHANGELOG.ja.md) を確認してください
