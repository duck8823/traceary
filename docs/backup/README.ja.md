# バックアップガイド

[English](./README.md)

Traceary の現時点の backup / export / import 導線は、意図的にシンプルにしています。

- サポートする export 形式は compact な SQLite バックアップファイルです
- `traceary backup create` でそのファイルを明示的に作成します
- `traceary backup restore` で Traceary の DB path へ復元し、必要なら migration を再適用します

現時点では JSON / CSV のような別形式 export はありません。

## バックアップを作成する

```sh
traceary backup create --output /tmp/traceary-backup.db
```

主な flag:

- `--db-path`: 既定以外の DB をバックアップしたいとき
- `--force`: 既存のバックアップファイルを上書きしたいとき

`backup create` は source DB が既に存在する前提です。まだ何も記録していない場合は、先に `traceary init` や通常の logging flow で DB を作ってください。

## バックアップから復元する

```sh
traceary backup restore --input /tmp/traceary-backup.db --force
```

主な flag:

- `--db-path`: 既定以外の復元先に入れたいとき
- `--force`: 既存の destination DB を上書きしたいとき
- `--yes`: 対話端末で既存 destination DB を上書きするときの確認を省略したいとき

復元では、まず backup file を destination path にコピーし、その後に通常の store initialization flow を通して newer migration を自動適用します。
`--force` を使う場合、restore は destination DB を破壊的に置き換える操作として扱い、必要なら先に新しい backup を取ってください。
対話端末では、`--yes` を付けない限り、上書き前に Traceary が確認を求めます。

## マシン移行の基本フロー

実用上は、次の流れを推奨します。

1. source machine で `traceary backup create --output /path/to/traceary-backup.db` を実行する
2. その SQLite file を普段使っている file transfer 手段で新しい machine にコピーする
3. destination machine で `traceary backup restore --input /path/to/traceary-backup.db --force` を実行する
4. 既定 path を使わない場合は、hooks / MCP client の DB path 設定も復元先に合わせる

## 運用上の注意

- バックアップファイルは line-oriented な export ではなく SQLite database です
- destination を上書きするのは `--force` を渡したときだけです
- destination DB path の解決順は通常どおり `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db` です
- マシン外に保存したい場合は、その SQLite file を既存の暗号化ディスク / backup tooling で保護してください

## この release でやらないこと

現時点では次はまだ入れていません。

- structured JSON / CSV export
- 一部イベントだけの partial import
- cloud backup / sync
