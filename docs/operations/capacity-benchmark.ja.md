# 容量診断とベンチマーク証跡

[English](capacity-benchmark.md)

`traceary store capacity` は `traceary.capacity/v1` JSON を出力します。ページ数、ファイルサイズ、SQLite オブジェクト名、payload サイズ区分の集計だけを含み、event body、prompt、transcript、command payload、workspace/session/event 識別子を選択・出力しません。

```sh
traceary store capacity --db-path ./traceary-copy.db > capacity.json
```

SQLite の任意機能 `dbstat` が利用できる場合、`evidence.status` は `complete` です。利用できない場合は `partial`、`evidence.method` は `pragma` となり、`objects` を省略します。ページ数、空きページ、payload サイズ区分、WAL サイズは引き続き取得します。

## 安全なベンチマーク手順

live store を直接ベンチマークしてはいけません。先に整合したコピーを作成します。

```sh
traceary store backup create /private/tmp/traceary-benchmark.db
go run ./cmd/store-benchmark --db /private/tmp/traceary-benchmark.db --iterations 25 > benchmark.json
```

copy mode は `immutable=1&mode=ro` で入力を開きます。cold はサンプルごとの新規 SQLite connection を意味し、OS cache の cold start を意味しません。host cache の消去も行いません。warm は同一 connection で直後に反復した query です。`traceary.store-benchmark/v1` JSON に `active`、`latest`、`handoff`、`search` の p50/p95（microseconds）と `EXPLAIN QUERY PLAN` を含めます。

合成 fixture は次のコマンドで作成します。

```sh
go run ./cmd/store-benchmark --synthetic /private/tmp/traceary-synthetic.db \
  --small-rows 10000 --large-rows 8 --iterations 25 > synthetic-benchmark.json
```

fixture は多数の小 row、少数の 1 MiB row、未 checkpoint の WAL、削除により生じる空きページを含みます。値はすべて生成文字列です。

## Sanitized 21.4 GiB shape baseline

移行前の基準 shape は **database allocation 21.4 GiB** です。整合したコピーから証跡を採取し、copy や raw row は commit しません。有効な baseline は、`capacity.json` に約 22,978,910,618 bytes の `database_bytes`、WAL/free-page、明示的な `dbstat` 完全性を記録し、`benchmark.json` に 4 case と plan を含めます。host、path、識別子、query value は意図的に除外します。timing は環境依存であり、hardware/cache 条件が異なる machine 間で単純比較しません。
