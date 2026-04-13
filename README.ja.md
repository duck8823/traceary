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
python3 ~/src/traceary/scripts/codex/install_plugin.py
```

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

## ホスト別の自動記録マトリクス

問い合わせ面は共通です。Traceary を入れれば、どのホストからでも同じ CLI / MCP の memory・context 機能を使えます。差が出るのは、hook でどこまで自動記録できるかです。

| ホスト | セッション境界 | ツール監査 | prompt 記録 | compact summary 記録 | 自動記録の対応レベル |
|---|---|---|---|---|---|
| Claude Code | 完全対応 | Bash + MCP + failure hook | あり | あり | Full |
| Codex | 完全対応（`SessionStart` + `Stop`） | tool hook | なし | なし | Partial |
| Gemini CLI | 完全対応（`SessionStart` + `SessionEnd`） | tool hook | なし | なし | Basic |

詳しい契約と hook の意味付けは、[Hook contract](./docs/hooks/contract.ja.md) と [イベントライフサイクル](./docs/lifecycle.ja.md) を参照してください。

## 先に知っておくと楽なこと

- `traceary log` / `traceary audit` で `--session-id` を省くと、解決できた repo / work context に対応する最新の non-stale アクティブセッションを優先して使います。`remote.origin.url` が無い Git worktree では、worktree ルートパスを代わりに使います
- `traceary session active` は既定で 24 時間を超えたセッションを stale とみなします。必要なら `--allow-stale` を付けてください
- `traceary session start` はセッション ID を出力し、`traceary session end` は記録したイベント ID を出力します
- `traceary session list --json` では、値がある場合に `label` / `summary` / `parent_session_id` も確認できます
- CLI の通常メッセージは英語が既定です。日本語表示にしたい場合は `TRACEARY_LANG=ja` を指定してください
- `--json` 出力は言語設定の影響を受けません

## ドキュメント

詳しい一覧は [ドキュメント索引](./docs/README.ja.md) にまとめています。最初によく使うのは次のページです。

- [ネイティブ連携ガイド](./docs/integrations/README.ja.md)
- [CLI リファレンス](./docs/cli/README.ja.md) — 手動で CLI を使う場合の詳細はこちら
- [Hooks ガイド](./docs/hooks/README.ja.md)
- [Hook contract と対応レベル](./docs/hooks/contract.ja.md)
- [イベントライフサイクル](./docs/lifecycle.ja.md)
- [MCP ガイド](./docs/mcp/README.ja.md)
- [環境変数と保存モデル](./docs/environment/README.ja.md)

## コントリビュートとサポート

- バグ報告や改善提案は GitHub Issues へお願いします
- 脆弱性の連絡先は [CONTRIBUTING.ja.md](./CONTRIBUTING.ja.md) を参照してください
- まだ `v0.x` の OSS なので、自動化に組み込む前には [変更履歴](./CHANGELOG.ja.md) を確認してください
