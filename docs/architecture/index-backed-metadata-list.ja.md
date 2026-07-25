# インデックス利用のメタデータ list/context クエリ

[English](index-backed-metadata-list.md)

## Structure-Behavior 設計メモ

### 要求

- 一般的なメタデータ list/context 読取は、RFC3339Nano の境界を正しく並べつつ全件一時ソートを行わない。
- workspace/session 絞り込みと offset ページングでも本文を読まない。
- スキーマ更新は追加だけで行い、アプリケーションのロールバック後も既存ストアを読める。

### 概念と責務

| 概念 | 責務 | 不変条件 |
| --- | --- | --- |
| 正規化時刻順 | 永続化した event 時刻列と SQLite インデックス | `created_at_norm, id` を唯一の順序契約にする |
| 絞り込み済みメタデータページ | metadata datasource | `events.body` を SELECT しない |
| インデックス配布 | migration 000031 | `created_at_norm` を追加・バックフィルし、trigger で維持する |

### 対応するクエリ計画

| クエリ形状 | 順序インデックス |
| --- | --- |
| 一般メタデータ list（limit/offset を含む） | `idx_events_created_at_norm_id_desc` |
| workspace list/context | `idx_events_workspace_created_at_norm_id_desc` |
| session list/context | `idx_events_session_created_at_norm_id_desc` |
| workspace + session context | `idx_events_workspace_session_created_at_norm_id_desc` |
| source-hook list | `idx_events_source_hook_created_at_norm_id_desc` |

公開済みフィルタの意味を保つため、list の条件は任意のままとする。ただし時刻境界が指定された場合は、順序インデックス内をseekできる直接範囲predicateのSQL variantを選ぶ。計画は順序インデックスを走査し、scope だけのページでは `offset + limit` に達したら終了する。scope 以外のフィルタはその順序走査中に適用する。組み合わせごとの SQL を増やして結果の意味を変えない。

### 振る舞いテスト

1. migration済みストアに対する代表的な list/context とlegacy source-hook fallbackの `EXPLAIN QUERY PLAN` が対象インデックスを使い、order-by 用の一時 B-tree を作らないことを確認する。
2. 通常 list と scope/offset ページの時刻順を確認する。
3. metadata SQL の SELECT リストで `body` と command payload 列を禁止する。
4. 000031 前のストアを更新し、固定幅時刻がバックフィルされ、insert と時刻更新後も維持されることを確認する。
5. 10k eventの直接range queryをCI smokeとしてp95 50 ms未満に保つ。migration済みDBのplan testでは、時刻の下限・上限が直接predicateであり、partial order-by sortを含む一時B-treeがないことも確認する。
6. release QAではopt-inのmulti-GiB benchmarkを実行する。これは一時ディレクトリにSQLiteが実際に管理するpageとevent bodyを作成し、`page_count * page_size`と`SUM(length(body))`を検証する。CIでは実行しない。

### 性能証跡

2026-07-25にGo 1.26.3、macOS 26.5（darwin/arm64）、modernc SQLite driverでCI smokeを測定した。index済みevent metadata 10,000行に対し、workspaceを絞った2秒の直接range、`limit=50`、25回反復のp95は**416.125us**で、目標は50 ms未満である。

release QAのopt-in benchmarkは256 MiB bodyを持つeventを8件（body合計2 GiB以上）作成し、測定前に`page_count * page_size >= 2 GiB`、event件数、body合計を検証する。その後、直接rangeを25回測定する。実行コマンドは次のとおり。

```sh
TRACEARY_RUN_MULTI_GIB_BENCHMARK=1 \
  go test ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkMetadataDirectRangeMultiGiB -benchtime=1x
```

p95の目標は250 ms未満である。2026-07-25にこの制約付きrunnerで実行を試みたが、fixture生成の完了前にprocessが終了したため、有効なmulti-GiB p95は記録できなかった。空き容量は7.8 GiBあったが、必要な長時間benchmark processを許可しないrunner制限である。release QAでは、このcommandを実行可能なhostで実行し、release前にp95を記録する。生成物はGo testの一時ディレクトリだけに置かれ、終了後に削除されるためcommitしない。またCIでは実行しない。full-scan退行は直接rangeのplan assertionを主な検出器とし、10k smokeのp95閾値はローカル遅延を検出する。

### ロールバックと残リスク

000031 は追加かつ冪等である。旧アプリは追加列、trigger、インデックスを無視するため、アプリのロールバック後もストアを読める。巨大ストアでのインデックス作成には一時的に容量と書込みロックが必要である。
