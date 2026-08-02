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

`capacity-baseline.sample.json` をコピーし、placeholder の timing と plan を sanitized な実測値へ置き換え、`go run ./cmd/store-benchmark --validate-baseline ./capacity-baseline.json` で検証します。validator は 21.4 GiB shape（許容差 256 MiB）、明示的な容量 evidence、passed case の正の timing、4 つの production query plan を要求します。path、bind value、host name、識別子を追加しません。

各 case は `--case-timeout`（default `2m`、minimum `1ms`）で制限します。plan は timed execution より先に取得するため、timeout でも sanitized `query_plan`、`timeout_ms`、right-censored `elapsed_lower_bound_us` を出力します。未完走の p50/p95 は捏造しません。case が1件でもtimeoutならreportもtimeoutです。診断証跡としては有効ですが、release performance targetを満たすのは観測済みp50/p95を持つ`passed`だけです。

完走した `search` case は privacy-safe な集計 `matched_rows` を含みます。0件も有効であり、body、query value、識別子は出力しません。

## Legacy/tiered search の全件 parity

追加の parity mode は、legacy の全 offset page と tiered の全 authenticated continuation を完走した後だけ membership set の一致を証明します。legacy search の authority は変更しません。private criteria は stdin の JSON、または permission が厳密に `0600` の regular file で渡します。

```sh
cat /private/tmp/private-parity-manifest.json | \
  go run ./cmd/store-benchmark --search-parity-manifest - > /private/tmp/search-parity.json
go run ./cmd/store-benchmark --validate-search-parity /private/tmp/search-parity.json
```

Manifest の必須 field は `db_path`、`query`、`legacy_page_size`、`tiered_page_size`、`source_rows`、`stored_bytes`、`decoded_bytes`、`timeout_ms`、`expected_revision`、`expected_dirty` です。optional filter は `workspace`、`session_id`、`client`、`agent`、`kind`、`from`、`to`、`failures_only` です。expected state は `git rev-parse HEAD` と `git status --porcelain --untracked-files=normal` から設定します。不一致なら store access より前に失敗します。evidence 自身が dirty-state assertion に自己参照しないよう、manifest と生成 artifact は repository の外に置きます。

出力は `traceary.search-parity/v1` と `comparison_contract: membership_set/v1` を使用します。revision/dirty state、membership/duplicate count、page/continuation count、projection revision/high-water、latency または right-censored elapsed lower bound、budget、aggregate logical/physical bytes だけを含みます。query、identifier、path、cursor、continuation、raw error text は出力しません。failure は fixed error class だけを使用します。status precedence は `failed > timeout > mismatch > passed` です。

Validator は unknown/privacy-forbidden field、trailing JSON、inconsistent metric、unknown error class を拒否します。timeout は diagnostic な right-censored evidence であり parity の証明ではありません。両 chain が duplicate なしで完走し、最終 membership set が一致した `passed` だけが parity を証明します。
