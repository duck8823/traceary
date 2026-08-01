# 容量診断とベンチマーク証跡

[English](capacity-benchmark.md)

`traceary store capacity` は `traceary.capacity/v1` JSON を出力します。ページ数、ファイルサイズ、SQLite オブジェクト名、payload サイズ区分の集計だけを含み、event body、prompt、transcript、command payload、workspace/session/event 識別子を選択・出力しません。

```sh
traceary store capacity --db-path ./traceary-copy.db > capacity.json
```

SQLite の任意機能 `dbstat` が利用できる場合、`evidence.status` は `complete` です。利用できない場合は `unavailable`、`evidence.method` は `pragma` となり、`objects` を省略します。予期しない dbstat error は command を失敗させます。payload 区分は `event_metadata_projection.body_stored_bytes` を使い、backfill 未完了時は `payload_evidence.status` が `partial` です。

## 安全なベンチマーク手順

live store を直接ベンチマークしてはいけません。先に整合したコピーを作成します。

```sh
traceary store backup create /private/tmp/traceary-benchmark.db
go run ./cmd/store-benchmark --db /private/tmp/traceary-benchmark.db --iterations 25 > benchmark.json
```

copy mode は `immutable=1&mode=ro` で入力を開きます。handoff の cold sample は production `ContextUsecase.Handoff` の全 datasource が共有する immutable connection group を毎回新規作成し、warm は同じ group 上で全 orchestration を直後に反復します。OS cache の cold start を意味せず、host cache も消去しません。`traceary.store-benchmark/v1` JSON に `active`、`latest`、`handoff`、`search` の p50/p95 と plan を含めます。

合成 fixture は次のコマンドで作成します。

```sh
go run ./cmd/store-benchmark --synthetic /private/tmp/traceary-synthetic.db \
  --small-rows 10000 --large-rows 8 --iterations 25 > synthetic-benchmark.json
```

fixture は canonical production migration と query source を使います。`--small-rows` 件の汎用小 row と `--large-rows` 件の 1 MiB row を保持し、別に disposable row 1,000 件を作成・削除します。さらに active lifecycle 1 件、command/audit 10 件、accepted workspace memory 10 件を含み、post-delete workload cardinality を JSON に記録します。active/latest が一致せず、または production handoff が command 10 件・memory 10 件を返さない場合、preflight は失敗します。

## Sanitized 21.4 GiB shape baseline

移行前の基準 shape は **database allocation 21.4 GiB** です。整合したコピーから証跡を採取し、copy や raw row は commit しません。有効な baseline は、`capacity.json` に約 22,978,910,618 bytes の `database_bytes`、WAL/free-page、明示的な `dbstat` 完全性を記録し、`benchmark.json` に 4 case と plan を含めます。host、path、識別子、query value は意図的に除外します。timing は環境依存であり、hardware/cache 条件が異なる machine 間で単純比較しません。

`capacity-baseline.sample.json` をコピーし、placeholder の timing と plan を sanitized な実測値へ置き換え、`go run ./cmd/store-benchmark --validate-baseline ./capacity-baseline.json` で検証します。validator は 21.4 GiB shape（許容差 256 MiB）、明示的な容量 evidence、正の timing、4 つの production query plan を要求します。path、bind value、host name、識別子を追加しません。
