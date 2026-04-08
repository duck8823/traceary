# Traceary

[English](./README.md)

<p align="center">
  <img src="./docs/assets/traceary-mark.svg" alt="Traceary mark" width="120">
</p>

[![CI](https://github.com/duck8823/traceary/actions/workflows/ci.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/ci.yml)
[![Release](https://github.com/duck8823/traceary/actions/workflows/release.yml/badge.svg)](https://github.com/duck8823/traceary/actions/workflows/release.yml)

Traceary は、AI エージェントの作業ログ、セッション境界、シェルコマンド監査をローカルの SQLite に残して検索できる CLI / MCP サーバーです。

## Traceary が必要になる場面

AI を使った開発では、次のような困りごとが起きがちです。

- `clear` や `compact` のあとにセッション文脈が消える
- Git 履歴だけでは「何を変えたか」は分かっても、「なぜそうしたか」が残りにくい
- どのエージェントがどのコマンドを実行したか追いにくい
- Claude、Codex、Gemini、手元のターミナル操作が別々に散らばる
- 並列セッションや worktree の移動で履歴の流れが見えにくくなる

Traceary は、こうした記録をひとつのローカルストアにまとめ、CLI・hooks・MCP から同じ履歴を使えるようにします。

## 記録するもの

- メモやレビュー記録
- セッション開始・終了イベント
- シェルコマンドの実行監査
- `client`、`agent`、`session_id`、リポジトリ/作業文脈などの付帯情報

Traceary はローカルファーストです。データは手元の SQLite に保存され、組み込みのテレメトリ、分析送信、ホスト型ストレージはありません。

## インストール

### go install

```sh
go install github.com/duck8823/traceary@latest
```

### Homebrew

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### 事前ビルド済みバイナリ

タグ付きリリースでは macOS / Linux 向けアーカイブを GitHub Releases に公開します。
配布形態の詳細は [リリースガイド](./docs/release/README.ja.md) を参照してください。

## クイックスタート

`traceary init` は必須ではありません。通常のコマンドを実行すれば、必要に応じて DB 作成とマイグレーションが自動で行われます。
`init` を使うのは、あらかじめ DB の保存先を用意したいときや、書き込み権限を先に確認したいときだけです。

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
traceary log --id-only "Investigating failing tests"
traceary audit --id-only --command "go test ./..." --input '{}' --output '{}'
traceary session end --session-id "$sid" --id-only
```

## 主なコマンド

```sh
traceary init
traceary log <message>
traceary audit [<command> <input> <output>]
traceary list
traceary search <query>
traceary show <event-id>
traceary context
traceary handoff
traceary session start
traceary session end
traceary session latest
traceary session active
traceary hooks print --client <claude|codex|gemini>
traceary hooks install --client <claude|codex|gemini>
traceary hooks guide --client <claude|codex|gemini>
traceary mcp-server
traceary doctor
traceary backup create --output <path>
traceary backup restore --input <path>
traceary gc
```

## 先に知っておくと楽なこと

- `traceary log` / `traceary audit` で `--session-id` を省くと、解決できた repo / work context に対する最新の non-stale アクティブなセッション を優先して使います
- `traceary session active` は既定で `24h` を超えたセッションを stale とみなします。必要なら `--allow-stale` を付けてください
- `traceary session start` はセッション ID を出力し、`traceary session end` は記録したイベント ID を出力します
- CLI の通常メッセージは英語が既定です。日本語表示にしたい場合は `TRACEARY_LANG=ja` を指定してください
- `--json` 出力は言語設定の影響を受けません

## ドキュメント

詳しい一覧は [ドキュメント索引](./docs/README.ja.md) にまとめています。
最初によく参照するのは次のページです。

- [CLI リファレンス](./docs/cli/README.ja.md)
- [Hooks ガイド](./docs/hooks/README.ja.md)
- [MCP ガイド](./docs/mcp/README.ja.md)
- [環境変数と保存モデル](./docs/environment/README.ja.md)

## コントリビュートとサポート

- バグ報告や改善提案は GitHub Issues へお願いします
- 脆弱性の連絡先は [CONTRIBUTING.ja.md](./CONTRIBUTING.ja.md) を参照してください
- まだ `v0.x` の OSS なので、自動化に組み込む前には [変更履歴](./CHANGELOG.ja.md) を確認してください
